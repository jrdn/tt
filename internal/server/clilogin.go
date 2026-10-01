package server

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/url"
	"time"
)

// `tt login` loopback flow (RFC 8252 with PKCE, RFC 7636). The CLI listens
// on 127.0.0.1, opens /auth/cli in the browser, and the user approves. The
// server redirects a one-time code to the CLI's listener, which redeems it
// at /auth/cli/token with its secret verifier. A phished link is harmless:
// the code lands on the victim's own machine, and without the verifier it
// can't be redeemed anyway.

const cliCodeTTL = 2 * time.Minute

// loopbackRedirect accepts only http://127.0.0.1:<port>/callback or the
// [::1] equivalent, so codes can never be sent off the user's machine.
func loopbackRedirect(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || u.Path != "/callback" || u.RawQuery != "" || u.User != nil || u.Fragment != "" {
		return nil, fmt.Errorf("redirect_uri must be http://127.0.0.1:<port>/callback")
	}
	ip := net.ParseIP(u.Hostname())
	if ip == nil || !ip.IsLoopback() || u.Port() == "" {
		return nil, fmt.Errorf("redirect_uri must be http://127.0.0.1:<port>/callback")
	}
	return u, nil
}

type cliLoginRequest struct {
	redirect   *url.URL
	state      string
	challenge  string
	clientName string
}

func parseCLILogin(get func(string) string) (*cliLoginRequest, error) {
	redirect, err := loopbackRedirect(get("redirect_uri"))
	if err != nil {
		return nil, err
	}
	req := &cliLoginRequest{redirect: redirect, state: get("state"), challenge: get("code_challenge"), clientName: get("client_name")}
	if req.state == "" || len(req.challenge) < 43 || get("code_challenge_method") != "S256" {
		return nil, fmt.Errorf("state and an S256 code_challenge are required")
	}
	return req, nil
}

// handleCLILoginPage asks the logged-in user to approve a CLI login.
func (s *Server) handleCLILoginPage(w http.ResponseWriter, r *http.Request) {
	req, err := parseCLILogin(r.URL.Query().Get)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	p := s.sessionUser(r)
	if p == nil {
		http.Redirect(w, r, "/auth/github/login?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!doctype html><title>tt login</title>
<form method="post" action="/auth/cli/approve">
<p>Log in to tt as <b>%s</b> from the tt CLI on <b>%s</b>?</p>
<input type="hidden" name="redirect_uri" value="%s">
<input type="hidden" name="state" value="%s">
<input type="hidden" name="code_challenge" value="%s">
<input type="hidden" name="code_challenge_method" value="S256">
<input type="hidden" name="client_name" value="%s">
<button>Approve</button></form>`,
		html.EscapeString(p.Handle), html.EscapeString(req.clientName), html.EscapeString(req.redirect.String()),
		html.EscapeString(req.state), html.EscapeString(req.challenge), html.EscapeString(req.clientName))
}

// handleCLILoginApprove issues a one-time code and redirects it to the CLI.
func (s *Server) handleCLILoginApprove(w http.ResponseWriter, r *http.Request) {
	if !s.sameOrigin(r) {
		writeErr(w, http.StatusForbidden, "cross-origin request")
		return
	}
	p := s.sessionUser(r)
	if p == nil {
		writeErr(w, http.StatusUnauthorized, "log in first")
		return
	}
	req, err := parseCLILogin(r.FormValue)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	b := make([]byte, 32)
	rand.Read(b)
	code := base64.RawURLEncoding.EncodeToString(b)
	if _, err := s.dir.db.ExecContext(r.Context(), `INSERT INTO cli_auth_codes
		(code_hash, principal_id, code_challenge, redirect_uri, client_name, expires_at) VALUES ($1, $2, $3, $4, $5, $6)`,
		hashCode(code), p.ID, req.challenge, req.redirect.String(), req.clientName, s.now().Add(cliCodeTTL)); err != nil {
		writeError(w, err)
		return
	}
	target := *req.redirect
	target.RawQuery = url.Values{"code": {code}, "state": {req.state}}.Encode()
	http.Redirect(w, r, target.String(), http.StatusFound)
}

// handleCLILoginToken redeems a code (once) for a user API key, given the
// PKCE verifier and the same redirect_uri.
func (s *Server) handleCLILoginToken(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Code        string `json:"code"`
		Verifier    string `json:"code_verifier"`
		RedirectURI string `json:"redirect_uri"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	var row struct {
		PrincipalID string    `db:"principal_id"`
		Challenge   string    `db:"code_challenge"`
		RedirectURI string    `db:"redirect_uri"`
		Client      string    `db:"client_name"`
		ExpiresAt   time.Time `db:"expires_at"`
	}
	// Delete on read: a code is redeemable once, whether or not this succeeds.
	err := s.dir.db.GetContext(r.Context(), &row, `DELETE FROM cli_auth_codes WHERE code_hash = $1
		RETURNING principal_id, code_challenge, redirect_uri, client_name, expires_at`, hashCode(body.Code))
	if errors.Is(err, sql.ErrNoRows) || err == nil && !s.now().Before(row.ExpiresAt) {
		writeErr(w, http.StatusBadRequest, "invalid or expired code; run tt login again")
		return
	}
	if err != nil {
		writeError(w, err)
		return
	}
	sum := sha256.Sum256([]byte(body.Verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	if subtle.ConstantTimeCompare([]byte(challenge), []byte(row.Challenge)) != 1 || body.RedirectURI != row.RedirectURI {
		writeErr(w, http.StatusBadRequest, "code_verifier or redirect_uri doesn't match")
		return
	}
	s.issueLoginKey(w, r, row.PrincipalID, row.Client)
}

// issueLoginKey mints the key a successful `tt login` receives.
func (s *Server) issueLoginKey(w http.ResponseWriter, r *http.Request, principalID, client string) {
	p, err := s.dir.PrincipalByID(r.Context(), principalID)
	if err != nil {
		writeError(w, err)
		return
	}
	k, key, err := s.dir.CreateKey(r.Context(), p, p, "tt login: "+client, nil, "", s.now().Add(s.cfg.LoginTTL))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"key": key, "key_id": k.ID, "handle": p.Handle, "expires_at": k.ExpiresAt})
}

// sameOrigin rejects browser requests sent from another site. Session
// cookies are SameSite=Lax, which already blocks cross-site POSTs; this is
// a second guard.
func (s *Server) sameOrigin(r *http.Request) bool {
	o := r.Header.Get("Origin")
	return o == "" || o == trimSlash(s.cfg.BaseURL)
}

func trimSlash(u string) string {
	for len(u) > 0 && u[len(u)-1] == '/' {
		u = u[:len(u)-1]
	}
	return u
}
