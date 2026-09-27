package server

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Device login lets `tt login` obtain a key without the CLI handling
// passwords or OAuth: the CLI starts a login and shows a short code, the
// user approves it in a logged-in browser, and the CLI's poll returns a
// user API key.

const deviceLoginTTL = 10 * time.Minute

// userCodeAlphabet avoids look-alike characters.
const userCodeAlphabet = "BCDFGHJKLMNPQRSTVWXZ23456789"

func newUserCode() string {
	b := make([]byte, 8)
	rand.Read(b)
	for i := range b {
		b[i] = userCodeAlphabet[int(b[i])%len(userCodeAlphabet)]
	}
	return string(b[:4]) + "-" + string(b[4:])
}

func hashCode(code string) []byte {
	h := sha256.Sum256([]byte(code))
	return h[:]
}

// handleDeviceStart begins a login for the CLI.
func (s *Server) handleDeviceStart(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ClientName string `json:"client_name"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	b := make([]byte, 32)
	rand.Read(b)
	deviceCode := base64.RawURLEncoding.EncodeToString(b)
	userCode := newUserCode()
	if _, err := s.dir.db.ExecContext(r.Context(), `INSERT INTO device_logins
		(device_code_hash, user_code, client_name, expires_at) VALUES ($1, $2, $3, $4)`,
		hashCode(deviceCode), userCode, body.ClientName, s.now().Add(deviceLoginTTL)); err != nil {
		writeError(w, err)
		return
	}
	verify := strings.TrimSuffix(s.cfg.BaseURL, "/") + "/auth/device"
	writeJSON(w, http.StatusOK, map[string]any{
		"device_code":               deviceCode,
		"user_code":                 userCode,
		"verification_uri":          verify,
		"verification_uri_complete": verify + "?user_code=" + url.QueryEscape(userCode),
		"expires_in":                int(deviceLoginTTL.Seconds()),
		"interval":                  2,
	})
}

// handleDevicePage shows the approval page, sending the user through
// GitHub login first if needed.
func (s *Server) handleDevicePage(w http.ResponseWriter, r *http.Request) {
	userCode := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("user_code")))
	p := s.sessionUser(r)
	if p == nil {
		http.Redirect(w, r, "/auth/github/login?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusFound)
		return
	}
	var client string
	err := s.dir.db.GetContext(r.Context(), &client, `SELECT client_name FROM device_logins
		WHERE user_code = $1 AND principal_id IS NULL AND expires_at > now()`, userCode)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err != nil {
		fmt.Fprint(w, `<!doctype html><title>tt login</title><form method="get">
			<p>Enter the code shown by <code>tt login</code>:</p>
			<input name="user_code" autofocus> <button>Continue</button></form>`)
		return
	}
	fmt.Fprintf(w, `<!doctype html><title>tt login</title><form method="post" action="/auth/device/approve">
		<p>Log in to tt as <b>%s</b> on <b>%s</b>?</p>
		<p>Code: <code>%s</code> (check it matches your terminal)</p>
		<input type="hidden" name="user_code" value="%s"><button>Approve</button></form>`,
		html.EscapeString(p.Handle), html.EscapeString(client), html.EscapeString(userCode), html.EscapeString(userCode))
}

// handleDeviceApprove marks a login approved by the browser's user. The
// session cookie is SameSite=Lax, so cross-site form posts can't reach
// here; the Origin check is a second guard.
func (s *Server) handleDeviceApprove(w http.ResponseWriter, r *http.Request) {
	if o := r.Header.Get("Origin"); o != "" && o != strings.TrimSuffix(s.cfg.BaseURL, "/") {
		writeErr(w, http.StatusForbidden, "cross-origin request")
		return
	}
	p := s.sessionUser(r)
	if p == nil {
		writeErr(w, http.StatusUnauthorized, "log in first")
		return
	}
	userCode := r.FormValue("user_code")
	res, err := s.dir.db.ExecContext(r.Context(), `UPDATE device_logins SET principal_id = $2
		WHERE user_code = $1 AND principal_id IS NULL AND expires_at > now()`, userCode, p.ID)
	if n, _ := res.RowsAffected(); err != nil || n == 0 {
		writeErr(w, http.StatusBadRequest, "unknown or expired code")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, `<!doctype html><title>tt login</title><p>Approved. Return to your terminal.</p>`)
}

// handleDevicePoll returns a user API key once the login is approved.
func (s *Server) handleDevicePoll(w http.ResponseWriter, r *http.Request) {
	var body struct {
		DeviceCode string `json:"device_code"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	var row struct {
		PrincipalID *string   `db:"principal_id"`
		Client      string    `db:"client_name"`
		ExpiresAt   time.Time `db:"expires_at"`
	}
	err := s.dir.db.GetContext(r.Context(), &row, `SELECT principal_id, client_name, expires_at
		FROM device_logins WHERE device_code_hash = $1`, hashCode(body.DeviceCode))
	switch {
	case errors.Is(err, sql.ErrNoRows):
		writeErr(w, http.StatusBadRequest, "expired_token")
		return
	case err != nil:
		writeError(w, err)
		return
	case !s.now().Before(row.ExpiresAt):
		writeErr(w, http.StatusBadRequest, "expired_token")
		return
	case row.PrincipalID == nil:
		writeErr(w, http.StatusBadRequest, "authorization_pending")
		return
	}
	// One-shot: delete before minting so a replayed poll gets nothing.
	res, err := s.dir.db.ExecContext(r.Context(), `DELETE FROM device_logins WHERE device_code_hash = $1`,
		hashCode(body.DeviceCode))
	if n, _ := res.RowsAffected(); err != nil || n == 0 {
		writeErr(w, http.StatusBadRequest, "expired_token")
		return
	}
	p, err := s.dir.PrincipalByID(r.Context(), *row.PrincipalID)
	if err != nil {
		writeError(w, err)
		return
	}
	k, key, err := s.dir.CreateKey(r.Context(), p, p, "tt login: "+row.Client, nil, "", s.now().Add(s.cfg.LoginTTL))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"key": key, "key_id": k.ID, "handle": p.Handle, "expires_at": k.ExpiresAt})
}

// sessionUser returns the logged-in browser user, or nil.
func (s *Server) sessionUser(r *http.Request) *Principal {
	ck, err := r.Cookie(sessionCookie)
	if err != nil {
		return nil
	}
	p, err := s.dir.SessionPrincipal(r.Context(), ck.Value)
	if err != nil {
		return nil
	}
	return p
}
