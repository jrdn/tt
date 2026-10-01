package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"slices"
	"strings"
	"time"

	"golang.org/x/oauth2"

	"github.com/jrdn/tt/internal/api"
	"github.com/jrdn/tt/internal/auth"
	ttdb "github.com/jrdn/tt/internal/db"
	"github.com/jrdn/tt/internal/task"
)

// errUnauthenticated marks credential failures, answered with 401.
var errUnauthenticated = errors.New("unauthenticated")

const sessionCookie = "tt_session"

type Config struct {
	DatabaseURL string
	MasterKey   []byte
	// BaseURL is the server's public URL, used for the OAuth callback.
	BaseURL string

	GitHubClientID     string
	GitHubClientSecret string
	// AdminLogins are GitHub logins granted server-admin on login.
	AdminLogins []string

	JWTTTL        time.Duration
	KeyDefaultTTL time.Duration
	KeyMaxTTL     time.Duration
	SessionTTL    time.Duration
	// LoginTTL is how long keys issued by `tt login` last.
	LoginTTL time.Duration
}

// Defaults fills unset lifetimes: 1h JWTs, 1-year keys, 30-day sessions,
// 90-day CLI logins.
func (c *Config) Defaults() {
	if c.JWTTTL == 0 {
		c.JWTTTL = time.Hour
	}
	if c.KeyDefaultTTL == 0 {
		c.KeyDefaultTTL = 365 * 24 * time.Hour
	}
	if c.KeyMaxTTL == 0 {
		c.KeyMaxTTL = c.KeyDefaultTTL
	}
	if c.SessionTTL == 0 {
		c.SessionTTL = 30 * 24 * time.Hour
	}
	if c.LoginTTL == 0 {
		c.LoginTTL = 90 * 24 * time.Hour
	}
}

type Server struct {
	cfg    Config
	dir    *Directory
	issuer *auth.Issuer
	github *oauth2.Config
	hub    *hub
	stop   context.CancelFunc
	now    func() time.Time
}

// New connects to Postgres, applies migrations and returns a server.
func New(ctx context.Context, cfg Config) (*Server, error) {
	cfg.Defaults()
	gdb, err := ttdb.OpenPostgres(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, err
	}
	iss, err := auth.NewIssuer(cfg.MasterKey, "tt", cfg.JWTTTL, 1)
	if err != nil {
		gdb.Close()
		return nil, err
	}
	s := &Server{
		cfg:    cfg,
		dir:    NewDirectory(gdb, cfg.DatabaseURL, cfg.MasterKey),
		issuer: iss,
		now:    time.Now,
	}
	if cfg.GitHubClientID != "" {
		s.github = githubOAuth(cfg)
	}
	// Live events run for the server's lifetime, not the caller's ctx.
	var hubCtx context.Context
	hubCtx, s.stop = context.WithCancel(context.Background())
	s.hub = newHub()
	go s.hub.run(hubCtx, cfg.DatabaseURL)
	select {
	case <-s.hub.ready:
	case <-time.After(5 * time.Second):
		log.Printf("live events: not listening yet; clients may miss updates until it connects")
	}
	return s, nil
}

func (s *Server) Directory() *Directory { return s.dir }

func (s *Server) Close() {
	s.stop()
	s.dir.Close()
	s.dir.db.Close()
}

// Handler returns the server's routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/jwks.json", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, s.issuer.JWKS())
	})
	mux.HandleFunc("POST /api/v1/token", s.handleToken)
	mux.HandleFunc("GET /auth/github/login", s.handleGitHubLogin)
	mux.HandleFunc("GET /auth/github/callback", s.handleGitHubCallback)
	mux.HandleFunc("POST /auth/logout", s.handleLogout)
	mux.HandleFunc("POST /auth/device/start", s.handleDeviceStart)
	mux.HandleFunc("GET /auth/device", s.handleDevicePage)
	mux.HandleFunc("POST /auth/device/confirm", s.handleDeviceConfirm)
	mux.HandleFunc("POST /auth/device/approve", s.handleDeviceApprove)
	mux.HandleFunc("GET /auth/cli", s.handleCLILoginPage)
	mux.HandleFunc("POST /auth/cli/approve", s.handleCLILoginApprove)
	mux.HandleFunc("POST /auth/cli/token", s.handleCLILoginToken)
	mux.HandleFunc("POST /auth/device/token", s.handleDevicePoll)
	mux.HandleFunc("GET /{$}", api.ServerIndex)

	authed := http.NewServeMux()
	authed.HandleFunc("GET /api/v1/whoami", s.handleWhoami)
	authed.HandleFunc("GET /api/v1/projects", s.handleListProjects)
	authed.HandleFunc("POST /api/v1/projects", s.handleCreateProject)
	authed.HandleFunc("PUT /api/v1/projects/{project}/members/{handle...}", s.handleSetMember)
	authed.HandleFunc("DELETE /api/v1/projects/{project}/members/{handle...}", s.handleRemoveMember)
	authed.HandleFunc("POST /api/v1/agents", s.handleCreateAgent)
	authed.HandleFunc("GET /api/v1/keys", s.handleListKeys)
	authed.HandleFunc("POST /api/v1/keys", s.handleCreateKey)
	authed.HandleFunc("DELETE /api/v1/keys/{id}", s.handleRevokeKey)
	tasks := &api.Server{Resolve: s.resolveProjectStore}
	tasks.Register(authed, "/api/v1/projects/{project}")

	mux.Handle("/api/v1/", s.authenticate(authed))
	return mux
}

// authenticate accepts a bearer JWT or a raw tt_ak_ API key. A raw key is
// exchanged inline, so everything downstream only sees verified claims.
// Cookies are accepted only at /api/v1/token, which keeps the API free of
// CSRF exposure.
func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bearer, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || bearer == "" {
			writeErr(w, http.StatusUnauthorized, "missing bearer token")
			return
		}
		var c *auth.Claims
		var err error
		if strings.HasPrefix(bearer, auth.KeyPrefix) {
			c, _, err = s.dir.ClaimsForKey(r.Context(), bearer, s.now())
			if err == nil {
				c.IssuedAt = nil // not a signed token; revoked_before doesn't apply
			}
		} else {
			c, err = s.issuer.Verify(bearer)
			if err != nil {
				err = fmt.Errorf("%w: %v", errUnauthenticated, err)
			}
		}
		if err == nil {
			err = s.dir.CheckLive(r.Context(), c)
		}
		if err != nil {
			writeError(w, err)
			return
		}
		next.ServeHTTP(w, r.WithContext(auth.WithClaims(r.Context(), c)))
	})
}

// handleToken exchanges an API key (bearer) or browser session (cookie) for
// a short-lived JWT.
func (s *Server) handleToken(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var c *auth.Claims
	var notAfter time.Time
	var err error
	if bearer, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
		c, notAfter, err = s.dir.ClaimsForKey(ctx, bearer, s.now())
	} else if ck, cerr := r.Cookie(sessionCookie); cerr == nil {
		var p *Principal
		if p, err = s.dir.SessionPrincipal(ctx, ck.Value); err == nil {
			c, err = s.dir.ClaimsForPrincipal(ctx, p)
		}
	} else {
		err = fmt.Errorf("%w: send an API key as a bearer token, or log in", errUnauthenticated)
	}
	if err != nil {
		writeError(w, err)
		return
	}
	tok, err := s.issuer.Sign(c, notAfter)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"token": tok, "expires_at": c.ExpiresAt.Time})
}

func (s *Server) handleWhoami(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, auth.FromContext(r.Context()))
}

// caller returns the authenticated principal.
func (s *Server) caller(r *http.Request) (*auth.Claims, *Principal, error) {
	c := auth.FromContext(r.Context())
	p, err := s.dir.PrincipalByID(r.Context(), c.Subject)
	return c, p, err
}

// project looks up a project the caller can see; others read as not found
// so their existence doesn't leak.
func (s *Server) project(r *http.Request) (*Project, error) {
	slug := r.PathValue("project")
	if _, ok := auth.FromContext(r.Context()).Projects[slug]; !ok {
		return nil, fmt.Errorf("%w: project %q", api.ErrNotFound, slug)
	}
	return s.dir.ProjectBySlug(r.Context(), slug)
}

func (s *Server) resolveProjectStore(r *http.Request) (task.Store, error) {
	p, err := s.project(r)
	if err != nil {
		return nil, err
	}
	pdb, err := s.dir.projectDB(r.Context(), p)
	if err != nil {
		return nil, err
	}
	return &authzStore{inner: task.NewSQLStore(pdb), project: p, dir: s.dir, hub: s.hub}, nil
}

func (s *Server) handleListProjects(w http.ResponseWriter, r *http.Request) {
	c := auth.FromContext(r.Context())
	slugs := []string{}
	for slug := range c.Projects {
		slugs = append(slugs, slug)
	}
	ps, err := s.dir.ProjectsBySlug(r.Context(), slugs)
	if err != nil {
		writeError(w, err)
		return
	}
	out := make([]map[string]any, len(ps))
	for i, p := range ps {
		out[i] = map[string]any{"slug": p.Slug, "name": p.Name, "role": c.Projects[p.Slug], "created_at": p.CreatedAt}
	}
	writeJSON(w, http.StatusOK, map[string]any{"projects": out})
}

func (s *Server) handleCreateProject(w http.ResponseWriter, r *http.Request) {
	c, p, err := s.caller(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if !c.Admin {
		writeError(w, fmt.Errorf("%w: only server admins can create projects", api.ErrForbidden))
		return
	}
	var body struct {
		Slug string `json:"slug"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Slug == "" {
		writeErr(w, http.StatusBadRequest, "slug is required")
		return
	}
	if body.Name == "" {
		body.Name = body.Slug
	}
	proj, err := s.dir.CreateProject(r.Context(), body.Slug, body.Name, p)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, proj)
}

func (s *Server) handleSetMember(w http.ResponseWriter, r *http.Request) {
	proj, member, ok := s.memberRequest(w, r)
	if !ok {
		return
	}
	var body struct {
		Role string `json:"role"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	role, err := auth.ParseRole(body.Role)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.dir.SetMember(r.Context(), proj, member, role); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"project": proj.Slug, "handle": member.Handle, "role": string(role)})
}

func (s *Server) handleRemoveMember(w http.ResponseWriter, r *http.Request) {
	proj, member, ok := s.memberRequest(w, r)
	if !ok {
		return
	}
	if err := s.dir.RemoveMember(r.Context(), proj, member); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// memberRequest resolves the project and member for membership changes,
// which need project:admin.
func (s *Server) memberRequest(w http.ResponseWriter, r *http.Request) (*Project, *Principal, bool) {
	c := auth.FromContext(r.Context())
	proj, err := s.project(r)
	if err == nil && !c.Can(proj.Slug, auth.ActProjectAdmin) {
		err = fmt.Errorf("%w: needs project:admin on %s", api.ErrForbidden, proj.Slug)
	}
	if err != nil {
		writeError(w, err)
		return nil, nil, false
	}
	member, err := s.dir.PrincipalByHandle(r.Context(), r.PathValue("handle"))
	if err != nil {
		writeError(w, err)
		return nil, nil, false
	}
	return proj, member, true
}

// requireUser allows only humans acting as themselves (not via an agent
// key) to manage agents and keys.
func (s *Server) requireUser(w http.ResponseWriter, r *http.Request) (*Principal, bool) {
	c, p, err := s.caller(r)
	if err == nil && (c.Kind != auth.KindUser || c.Label != "" || c.Actions != nil || c.TaskScope != nil) {
		err = fmt.Errorf("%w: only users can manage agents and keys", api.ErrForbidden)
	}
	if err != nil {
		writeError(w, err)
		return nil, false
	}
	return p, true
}

func (s *Server) handleCreateAgent(w http.ResponseWriter, r *http.Request) {
	owner, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	var body struct {
		Handle string `json:"handle"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Handle == "" {
		writeErr(w, http.StatusBadRequest, "handle is required")
		return
	}
	a, err := s.dir.CreateAgent(r.Context(), owner, body.Handle)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": a.ID, "handle": a.Handle, "owner": owner.Handle})
}

func (s *Server) handleListKeys(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	keys, err := s.dir.KeysOwnedBy(r.Context(), user.ID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"keys": keys})
}

// handleCreateKey mints a root key for the caller or one of their agents.
func (s *Server) handleCreateKey(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	var body struct {
		Agent    string   `json:"agent"` // empty = a key for the user
		Name     string   `json:"name"`
		Projects []string `json:"projects"`
		Role     string   `json:"role"`
		Expires  string   `json:"expires"` // Go duration, e.g. "720h"
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	target := user
	if body.Agent != "" {
		a, err := s.dir.PrincipalByHandle(r.Context(), body.Agent)
		if err != nil {
			writeError(w, err)
			return
		}
		if a.OwnerID == nil || *a.OwnerID != user.ID {
			writeError(w, fmt.Errorf("%w: %s is not your agent", api.ErrForbidden, body.Agent))
			return
		}
		target = a
	}
	var role auth.Role
	if body.Role == "" && target.Kind == auth.KindAgent {
		// Agents don't get owner rights (managing members) unless asked.
		role = auth.RoleMember
	}
	if body.Role != "" {
		var err error
		if role, err = auth.ParseRole(body.Role); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	ttl := s.cfg.KeyDefaultTTL
	if body.Expires != "" {
		d, err := time.ParseDuration(body.Expires)
		if err != nil || d <= 0 {
			writeErr(w, http.StatusBadRequest, "expires must be a positive duration like 720h")
			return
		}
		ttl = d
	}
	if ttl > s.cfg.KeyMaxTTL {
		writeErr(w, http.StatusBadRequest, fmt.Sprintf("expires exceeds the server maximum of %s", s.cfg.KeyMaxTTL))
		return
	}
	var scopes []string
	if body.Projects != nil {
		scopes = slices.Clone(body.Projects)
	}
	k, key, err := s.dir.CreateKey(r.Context(), target, user, body.Name, scopes, role, s.now().Add(ttl))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"key": key, "record": k})
}

func (s *Server) handleRevokeKey(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	k, err := s.dir.KeyByID(r.Context(), r.PathValue("id"))
	if err == nil {
		var owner *Principal
		if owner, err = s.dir.PrincipalByID(r.Context(), k.PrincipalID); err == nil &&
			owner.ID != user.ID && (owner.OwnerID == nil || *owner.OwnerID != user.ID) {
			err = fmt.Errorf("%w: key %s", api.ErrNotFound, k.ID)
		}
	}
	if err == nil {
		err = s.dir.RevokeKey(r.Context(), k.ID)
	}
	if err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// writeError maps sentinel errors to status codes.
func writeError(w http.ResponseWriter, err error) {
	code := http.StatusInternalServerError
	switch {
	case errors.Is(err, errUnauthenticated):
		code = http.StatusUnauthorized
	case errors.Is(err, errHandleTaken):
		code = http.StatusConflict
	case errors.Is(err, api.ErrForbidden):
		code = http.StatusForbidden
	case errors.Is(err, api.ErrNotFound):
		code = http.StatusNotFound
	}
	writeErr(w, code, err.Error())
}
