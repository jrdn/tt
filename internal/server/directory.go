// Package server implements tt server: the multiuser HTTP API over Postgres,
// with GitHub login, macaroon API keys and per-project authorization.
package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/jrdn/tt/internal/api"
	"github.com/jrdn/tt/internal/auth"
	ttdb "github.com/jrdn/tt/internal/db"
	"github.com/jrdn/tt/internal/task"
)

// Principal is anything that can act or be assigned.
type Principal struct {
	ID            string     `db:"id"`
	Kind          auth.Kind  `db:"kind"`
	Handle        string     `db:"handle"`
	OwnerID       *string    `db:"owner_id"`
	CreatedAt     time.Time  `db:"created_at"`
	DisabledAt    *time.Time `db:"disabled_at"`
	RevokedBefore *time.Time `db:"revoked_before"`
}

type Project struct {
	ID         string    `db:"id"          json:"id"`
	Slug       string    `db:"slug"        json:"slug"`
	Name       string    `db:"name"        json:"name"`
	SchemaName string    `db:"schema_name" json:"-"`
	CreatedAt  time.Time `db:"created_at"  json:"created_at"`
}

type APIKey struct {
	ID            string     `db:"id"             json:"id"`
	PrincipalID   string     `db:"principal_id"   json:"-"`
	Handle        string     `db:"handle"         json:"handle"`
	CreatedBy     string     `db:"created_by"     json:"-"`
	Name          string     `db:"name"           json:"name"`
	ProjectScopes Scopes     `db:"project_scopes" json:"projects"`
	MaxRole       *string    `db:"max_role"       json:"max_role"`
	ExpiresAt     *time.Time `db:"expires_at"     json:"expires_at"`
	CreatedAt     time.Time  `db:"created_at"     json:"created_at"`
	LastUsedAt    *time.Time `db:"last_used_at"   json:"last_used_at"`
	RevokedAt     *time.Time `db:"revoked_at"     json:"revoked_at"`
}

// Scopes is a key's project list, stored comma-separated (slugs can't
// contain commas). nil means every project the principal can reach.
type Scopes []string

func (s *Scopes) Scan(v any) error {
	switch v := v.(type) {
	case nil:
		*s = nil
	case string:
		*s = Scopes{}
		if v != "" {
			*s = strings.Split(v, ",")
		}
	default:
		return fmt.Errorf("scan scopes: unexpected %T", v)
	}
	return nil
}

func (s Scopes) Value() (driver.Value, error) {
	if s == nil {
		return nil, nil
	}
	return strings.Join(s, ","), nil
}

// Directory manages identities, projects, keys and sessions in the global
// schema, and opens each project's task store.
type Directory struct {
	db     *sqlx.DB
	dbURL  string
	master []byte

	mu     sync.Mutex
	stores map[string]*sqlx.DB // schema name -> pool
}

func NewDirectory(db *sqlx.DB, dbURL string, master []byte) *Directory {
	return &Directory{db: db, dbURL: dbURL, master: master, stores: map[string]*sqlx.DB{}}
}

func (d *Directory) Close() {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, s := range d.stores {
		s.Close()
	}
}

// ---- principals ----

const principalCols = `id, kind, handle, owner_id, created_at, disabled_at, revoked_before`

func (d *Directory) PrincipalByID(ctx context.Context, id string) (*Principal, error) {
	var p Principal
	err := d.db.GetContext(ctx, &p, `SELECT `+principalCols+` FROM principals WHERE id = $1`, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: principal %q", api.ErrNotFound, id)
	}
	return &p, err
}

func (d *Directory) PrincipalByHandle(ctx context.Context, handle string) (*Principal, error) {
	var p Principal
	err := d.db.GetContext(ctx, &p, `SELECT `+principalCols+` FROM principals WHERE handle = $1`, handle)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: no principal %q", api.ErrNotFound, handle)
	}
	return &p, err
}

// errHandleTaken means a GitHub sign-in's login is already used as the handle
// of an account not linked to that GitHub user.
var errHandleTaken = errors.New("handle taken")

// CreateUser adds a human principal. githubID is nil for users created by
// the operator CLI; LinkGitHub can later attach their GitHub account.
func (d *Directory) CreateUser(ctx context.Context, handle string, githubID *int64, admin bool) (*Principal, error) {
	tx, err := d.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	p := &Principal{ID: "prn_" + task.NewID() + task.NewID(), Kind: auth.KindUser, Handle: handle}
	if err := tx.GetContext(ctx, &p.CreatedAt,
		`INSERT INTO principals (id, kind, handle) VALUES ($1, 'user', $2) RETURNING created_at`,
		p.ID, handle); err != nil {
		return nil, fmt.Errorf("create user %q: %w", handle, err)
	}
	// Only GitHub-created users record a login here: for users without a
	// GitHub ID, github_login is an explicit link request (see LinkGitHub).
	var login *string
	if githubID != nil {
		login = &handle
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO users (principal_id, github_id, github_login, is_admin) VALUES ($1, $2, $3, $4)`,
		p.ID, githubID, login, admin); err != nil {
		return nil, err
	}
	return p, tx.Commit()
}

// LinkGitHub arranges for the first GitHub sign-in as login to use the
// existing user with this handle, keeping its keys, projects and history.
// GitHub logins are matched case-insensitively.
func (d *Directory) LinkGitHub(ctx context.Context, handle, login string) error {
	p, err := d.PrincipalByHandle(ctx, handle)
	if err != nil {
		return err
	}
	if p.Kind != auth.KindUser {
		return fmt.Errorf("%s is an %s, not a user", handle, p.Kind)
	}
	var taken string
	err = d.db.GetContext(ctx, &taken, `SELECT p.handle FROM users u JOIN principals p ON p.id = u.principal_id
		WHERE lower(u.github_login) = lower($1) AND u.principal_id <> $2`, login, p.ID)
	if err == nil {
		return fmt.Errorf("GitHub login %s is already linked to %s", login, taken)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	res, err := d.db.ExecContext(ctx, `UPDATE users SET github_login = $2 WHERE principal_id = $1 AND github_id IS NULL`,
		p.ID, login)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%s has already signed in with GitHub; its link can't be changed", handle)
	}
	return nil
}

// UpsertGitHubUser finds or creates the user for a GitHub account. admin
// promotes (never demotes) the user.
func (d *Directory) UpsertGitHubUser(ctx context.Context, githubID int64, login, email, name string, admin bool) (*Principal, error) {
	var id string
	err := d.db.GetContext(ctx, &id, `UPDATE users
		SET github_login = $2, email = NULLIF($3, ''), name = NULLIF($4, ''), is_admin = is_admin OR $5
		WHERE github_id = $1 RETURNING principal_id`, githubID, login, email, name, admin)
	if err == nil {
		return d.PrincipalByID(ctx, id)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	// First sign-in for a user the operator linked to this login.
	err = d.db.GetContext(ctx, &id, `UPDATE users
		SET github_id = $1, github_login = $2, email = NULLIF($3, ''), name = NULLIF($4, ''), is_admin = is_admin OR $5
		WHERE github_id IS NULL AND lower(github_login) = lower($2) RETURNING principal_id`,
		githubID, login, email, name, admin)
	if err == nil {
		return d.PrincipalByID(ctx, id)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if _, err := d.PrincipalByHandle(ctx, login); err == nil {
		return nil, fmt.Errorf("%w: the tt handle %q already belongs to another account. If it's yours, "+
			"the server operator can link it with: tt server link-github %s %s", errHandleTaken, login, login, login)
	}
	p, err := d.CreateUser(ctx, login, &githubID, admin)
	if err != nil {
		return nil, err
	}
	_, err = d.db.ExecContext(ctx, `UPDATE users SET email = NULLIF($2, ''), name = NULLIF($3, '') WHERE principal_id = $1`,
		p.ID, email, name)
	return p, err
}

func (d *Directory) IsAdmin(ctx context.Context, principalID string) (bool, error) {
	var admin bool
	err := d.db.GetContext(ctx, &admin, `SELECT is_admin FROM users WHERE principal_id = $1`, principalID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return admin, err
}

// CreateAgent adds an agent principal owned by a user.
func (d *Directory) CreateAgent(ctx context.Context, owner *Principal, handle string) (*Principal, error) {
	if owner.Kind != auth.KindUser {
		return nil, fmt.Errorf("%w: only users can own agents", api.ErrForbidden)
	}
	p := &Principal{ID: "prn_" + task.NewID() + task.NewID(), Kind: auth.KindAgent, Handle: handle, OwnerID: &owner.ID}
	err := d.db.GetContext(ctx, &p.CreatedAt,
		`INSERT INTO principals (id, kind, handle, owner_id) VALUES ($1, 'agent', $2, $3) RETURNING created_at`,
		p.ID, handle, owner.ID)
	if err != nil {
		return nil, fmt.Errorf("create agent %q: %w", handle, err)
	}
	return p, nil
}

// RevokeTokens rejects every token already issued to the principal and to
// agents it owns. Called when access is reduced so stale JWTs stop working.
func (d *Directory) RevokeTokens(ctx context.Context, principalID string) error {
	_, err := d.db.ExecContext(ctx,
		`UPDATE principals SET revoked_before = now() WHERE id = $1 OR owner_id = $1`, principalID)
	return err
}

// ---- projects ----

func (d *Directory) CreateProject(ctx context.Context, slug, name string, creator *Principal) (*Project, error) {
	if creator.Kind != auth.KindUser {
		return nil, fmt.Errorf("%w: only users can create projects", api.ErrForbidden)
	}
	p := &Project{ID: "prj_" + task.NewID(), Slug: slug, Name: name, SchemaName: "p_" + strings.ReplaceAll(slug, "-", "_")}
	tx, err := d.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := tx.GetContext(ctx, &p.CreatedAt,
		`INSERT INTO projects (id, slug, name, schema_name) VALUES ($1, $2, $3, $4) RETURNING created_at`,
		p.ID, p.Slug, p.Name, p.SchemaName); err != nil {
		return nil, fmt.Errorf("create project %q: %w", slug, err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO project_members (project_id, principal_id, role) VALUES ($1, $2, 'owner')`,
		p.ID, creator.ID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	// Create the task schema now so problems surface at creation time.
	if _, err := d.projectDB(ctx, p); err != nil {
		return nil, err
	}
	return p, nil
}

func (d *Directory) ProjectBySlug(ctx context.Context, slug string) (*Project, error) {
	var p Project
	err := d.db.GetContext(ctx, &p, `SELECT id, slug, name, schema_name, created_at FROM projects WHERE slug = $1`, slug)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: project %q", api.ErrNotFound, slug)
	}
	return &p, err
}

func (d *Directory) ProjectsBySlug(ctx context.Context, slugs []string) ([]Project, error) {
	ps := []Project{}
	err := d.db.SelectContext(ctx, &ps,
		`SELECT id, slug, name, schema_name, created_at FROM projects WHERE slug = ANY($1) ORDER BY slug`,
		slugs)
	return ps, err
}

// SetMember adds or changes a member's role. Lowering a role revokes the
// member's outstanding tokens.
func (d *Directory) SetMember(ctx context.Context, project *Project, principal *Principal, role auth.Role) error {
	if principal.Kind == auth.KindAgent {
		return fmt.Errorf("agents inherit their owner's access; add the owner instead")
	}
	var prev sql.NullString
	err := d.db.GetContext(ctx, &prev, `SELECT role FROM project_members WHERE project_id = $1 AND principal_id = $2`,
		project.ID, principal.ID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if _, err := d.db.ExecContext(ctx, `INSERT INTO project_members (project_id, principal_id, role) VALUES ($1, $2, $3)
		ON CONFLICT (project_id, principal_id) DO UPDATE SET role = excluded.role`,
		project.ID, principal.ID, role); err != nil {
		return err
	}
	if prev.Valid && auth.MinRole(auth.Role(prev.String), role) == role && auth.Role(prev.String) != role {
		return d.RevokeTokens(ctx, principal.ID)
	}
	return nil
}

func (d *Directory) RemoveMember(ctx context.Context, project *Project, principal *Principal) error {
	if _, err := d.db.ExecContext(ctx, `DELETE FROM project_members WHERE project_id = $1 AND principal_id = $2`,
		project.ID, principal.ID); err != nil {
		return err
	}
	return d.RevokeTokens(ctx, principal.ID)
}

// Roles returns the principal's role in each project, keyed by slug. Agents
// get their owner's roles.
func (d *Directory) Roles(ctx context.Context, p *Principal) (map[string]auth.Role, error) {
	memberID := p.ID
	if p.Kind == auth.KindAgent {
		memberID = *p.OwnerID
	}
	var rows []struct {
		Slug string `db:"slug"`
		Role string `db:"role"`
	}
	if err := d.db.SelectContext(ctx, &rows, `SELECT p.slug, m.role FROM project_members m
		JOIN projects p ON p.id = m.project_id WHERE m.principal_id = $1`, memberID); err != nil {
		return nil, err
	}
	roles := map[string]auth.Role{}
	for _, r := range rows {
		roles[r.Slug] = auth.Role(r.Role)
	}
	return roles, nil
}

// projectDB returns the connection pool for a project's task schema,
// opening (and migrating) it on first use.
func (d *Directory) projectDB(ctx context.Context, p *Project) (*sqlx.DB, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if s, ok := d.stores[p.SchemaName]; ok {
		return s, nil
	}
	s, err := ttdb.OpenProjectSchema(ctx, d.dbURL, p.SchemaName)
	if err != nil {
		return nil, fmt.Errorf("open project %q: %w", p.Slug, err)
	}
	s.SetMaxOpenConns(4)
	d.stores[p.SchemaName] = s
	return s, nil
}

// ---- API keys ----

// CreateKey stores a root key record for principal and returns the key.
// scopes nil means every project the principal can reach.
func (d *Directory) CreateKey(ctx context.Context, principal, createdBy *Principal, name string,
	scopes []string, maxRole auth.Role, expires time.Time) (*APIKey, string, error) {
	k := &APIKey{ID: "key_" + task.NewID() + task.NewID(), PrincipalID: principal.ID, Handle: principal.Handle,
		CreatedBy: createdBy.ID, Name: name}
	if scopes != nil {
		k.ProjectScopes = Scopes(scopes)
	}
	if maxRole != "" {
		r := string(maxRole)
		k.MaxRole = &r
	}
	if !expires.IsZero() {
		k.ExpiresAt = &expires
	}
	if err := d.db.GetContext(ctx, &k.CreatedAt, `INSERT INTO api_keys
		(id, principal_id, created_by, name, project_scopes, max_role, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING created_at`,
		k.ID, k.PrincipalID, k.CreatedBy, k.Name, k.ProjectScopes, k.MaxRole, k.ExpiresAt); err != nil {
		return nil, "", err
	}
	key, err := auth.MintKey(d.master, k.ID)
	return k, key, err
}

const keyCols = `k.id, k.principal_id, p.handle, k.created_by, k.name, k.project_scopes, k.max_role,
	k.expires_at, k.created_at, k.last_used_at, k.revoked_at`

func (d *Directory) KeyByID(ctx context.Context, id string) (*APIKey, error) {
	var k APIKey
	err := d.db.GetContext(ctx, &k, `SELECT `+keyCols+` FROM api_keys k
		JOIN principals p ON p.id = k.principal_id WHERE k.id = $1`, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: key %q", api.ErrNotFound, id)
	}
	return &k, err
}

// KeysOwnedBy lists keys for a user and the agents they own.
func (d *Directory) KeysOwnedBy(ctx context.Context, userID string) ([]APIKey, error) {
	keys := []APIKey{}
	err := d.db.SelectContext(ctx, &keys, `SELECT `+keyCols+` FROM api_keys k
		JOIN principals p ON p.id = k.principal_id
		WHERE p.id = $1 OR p.owner_id = $1 ORDER BY k.created_at`, userID)
	return keys, err
}

func (d *Directory) RevokeKey(ctx context.Context, id string) error {
	_, err := d.db.ExecContext(ctx, `UPDATE api_keys SET revoked_at = now() WHERE id = $1 AND revoked_at IS NULL`, id)
	return err
}

// ---- claims ----

// ClaimsForPrincipal builds the claims for a principal with no key limits
// (browser sessions).
func (d *Directory) ClaimsForPrincipal(ctx context.Context, p *Principal) (*auth.Claims, error) {
	if p.DisabledAt != nil {
		return nil, fmt.Errorf("%w: principal disabled", api.ErrForbidden)
	}
	roles, err := d.Roles(ctx, p)
	if err != nil {
		return nil, err
	}
	c := &auth.Claims{Kind: p.Kind, Handle: p.Handle, Projects: roles}
	c.Subject = p.ID
	if p.Kind == auth.KindUser {
		if c.Admin, err = d.IsAdmin(ctx, p.ID); err != nil {
			return nil, err
		}
	}
	if p.OwnerID != nil {
		owner, err := d.PrincipalByID(ctx, *p.OwnerID)
		if err != nil {
			return nil, err
		}
		if owner.DisabledAt != nil {
			return nil, fmt.Errorf("%w: owner disabled", api.ErrForbidden)
		}
		c.OnBehalfOf = owner.Handle
	}
	return c, nil
}

// ClaimsForKey verifies an API key (root or attenuated) and returns the
// claims it grants: the principal's current roles, narrowed by the key
// record and by every caveat. notAfter is when the key itself expires.
func (d *Directory) ClaimsForKey(ctx context.Context, key string, now time.Time) (c *auth.Claims, notAfter time.Time, err error) {
	keyID, restr, err := auth.VerifyKey(d.master, key, now)
	if err != nil {
		return nil, notAfter, fmt.Errorf("%w: %v", errUnauthenticated, err)
	}
	k, err := d.KeyByID(ctx, keyID)
	if err != nil {
		return nil, notAfter, fmt.Errorf("%w: unknown api key", errUnauthenticated)
	}
	if k.RevokedAt != nil {
		return nil, notAfter, fmt.Errorf("%w: api key revoked", errUnauthenticated)
	}
	if k.ExpiresAt != nil && !now.Before(*k.ExpiresAt) {
		return nil, notAfter, fmt.Errorf("%w: api key expired", errUnauthenticated)
	}
	p, err := d.PrincipalByID(ctx, k.PrincipalID)
	if err != nil {
		return nil, notAfter, err
	}
	c, err = d.ClaimsForPrincipal(ctx, p)
	if err != nil {
		return nil, notAfter, err
	}
	// Server-admin rights survive only on a user's own unrestricted key.
	if restr.Projects != nil || restr.MaxRole != "" || restr.Tasks != nil || restr.Actions != nil || restr.Label != "" ||
		k.ProjectScopes != nil || k.MaxRole != nil {
		c.Admin = false
	}

	var keyRole auth.Role
	if k.MaxRole != nil {
		keyRole = auth.Role(*k.MaxRole)
	}
	narrowed := map[string]auth.Role{}
	for slug, role := range c.Projects {
		if k.ProjectScopes != nil && !slices.Contains(k.ProjectScopes, slug) {
			continue
		}
		if restr.Projects != nil && !slices.Contains(restr.Projects, slug) {
			continue
		}
		narrowed[slug] = auth.MinRole(auth.MinRole(role, keyRole), restr.MaxRole)
	}
	c.Projects = narrowed
	c.TaskScope = restr.Tasks
	c.Actions = restr.Actions
	c.Label = restr.Label
	c.KeyID = k.ID

	if k.ExpiresAt != nil {
		notAfter = *k.ExpiresAt
	}
	if !restr.Expires.IsZero() && (notAfter.IsZero() || restr.Expires.Before(notAfter)) {
		notAfter = restr.Expires
	}
	d.db.ExecContext(ctx, `UPDATE api_keys SET last_used_at = now() WHERE id = $1`, k.ID)
	return c, notAfter, nil
}

// CheckLive rejects claims whose principal was disabled, whose tokens were
// revoked after issue, or whose key was revoked.
func (d *Directory) CheckLive(ctx context.Context, c *auth.Claims) error {
	var row struct {
		Disabled      *time.Time `db:"disabled_at"`
		RevokedBefore *time.Time `db:"revoked_before"`
		OwnerDisabled *time.Time `db:"owner_disabled_at"`
		KeyRevoked    *time.Time `db:"key_revoked_at"`
		KeyExpires    *time.Time `db:"key_expires_at"`
	}
	err := d.db.GetContext(ctx, &row, `SELECT p.disabled_at, p.revoked_before,
			o.disabled_at AS owner_disabled_at, k.revoked_at AS key_revoked_at, k.expires_at AS key_expires_at
		FROM principals p
		LEFT JOIN principals o ON o.id = p.owner_id
		LEFT JOIN api_keys k ON k.id = $2
		WHERE p.id = $1`, c.Subject, c.KeyID)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: unknown principal", errUnauthenticated)
	}
	if err != nil {
		return err
	}
	switch {
	case row.Disabled != nil, row.OwnerDisabled != nil:
		return fmt.Errorf("%w: principal disabled", errUnauthenticated)
	case row.KeyRevoked != nil:
		return fmt.Errorf("%w: api key revoked", errUnauthenticated)
	case row.KeyExpires != nil && !time.Now().Before(*row.KeyExpires):
		return fmt.Errorf("%w: api key expired", errUnauthenticated)
	case row.RevokedBefore != nil && c.IssuedAt != nil && !c.IssuedAt.Time.After(*row.RevokedBefore):
		return fmt.Errorf("%w: token revoked", errUnauthenticated)
	}
	return nil
}

// ---- sessions ----

func (d *Directory) CreateSession(ctx context.Context, principalID string, ttl time.Duration) (string, error) {
	b := make([]byte, 32)
	rand.Read(b)
	token := base64.RawURLEncoding.EncodeToString(b)
	h := sha256.Sum256([]byte(token))
	_, err := d.db.ExecContext(ctx, `INSERT INTO sessions (token_hash, principal_id, expires_at) VALUES ($1, $2, $3)`,
		h[:], principalID, time.Now().Add(ttl))
	return token, err
}

func (d *Directory) SessionPrincipal(ctx context.Context, token string) (*Principal, error) {
	h := sha256.Sum256([]byte(token))
	var id string
	err := d.db.GetContext(ctx, &id, `SELECT principal_id FROM sessions WHERE token_hash = $1 AND expires_at > now()`, h[:])
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: no session", errUnauthenticated)
	}
	if err != nil {
		return nil, err
	}
	return d.PrincipalByID(ctx, id)
}

func (d *Directory) DeleteSession(ctx context.Context, token string) error {
	h := sha256.Sum256([]byte(token))
	_, err := d.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = $1`, h[:])
	return err
}

// ---- audit ----

// RecordEvent appends to the audit log. Failures are returned but callers
// treat them as non-fatal: the change itself has already been made.
func (d *Directory) RecordEvent(ctx context.Context, project *Project, c *auth.Claims, taskID, action string, detail map[string]any) error {
	var detailJSON []byte
	if detail != nil {
		var err error
		if detailJSON, err = json.Marshal(detail); err != nil {
			return err
		}
	}
	_, err := d.db.ExecContext(ctx, `INSERT INTO task_events (project_id, task_id, actor_id, actor_label, token_id, action, detail)
		VALUES ($1, $2, $3, NULLIF($4, ''), NULLIF($5, ''), $6, $7)`,
		project.ID, taskID, c.Subject, c.Label, c.ID, action, detailJSON)
	return err
}
