package server

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/github"
)

const (
	oauthStateCookie = "tt_oauth_state"
	oauthNextCookie  = "tt_oauth_next"
)

// safeNext accepts only same-site paths, so login can't be used as an open
// redirect.
func safeNext(next string) string {
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.HasPrefix(next, "/\\") {
		return "/"
	}
	return next
}

func githubOAuth(cfg Config) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     cfg.GitHubClientID,
		ClientSecret: cfg.GitHubClientSecret,
		Endpoint:     github.Endpoint,
		RedirectURL:  strings.TrimSuffix(cfg.BaseURL, "/") + "/auth/github/callback",
		Scopes:       []string{"read:user", "user:email"},
	}
}

func (s *Server) secureCookies() bool {
	return strings.HasPrefix(s.cfg.BaseURL, "https://")
}

func (s *Server) handleGitHubLogin(w http.ResponseWriter, r *http.Request) {
	if s.github == nil {
		writeErr(w, http.StatusNotFound, "GitHub login is not configured")
		return
	}
	b := make([]byte, 16)
	rand.Read(b)
	state := base64.RawURLEncoding.EncodeToString(b)
	http.SetCookie(w, &http.Cookie{
		Name: oauthStateCookie, Value: state, Path: "/auth/github",
		MaxAge: 600, HttpOnly: true, Secure: s.secureCookies(), SameSite: http.SameSiteLaxMode,
	})
	http.SetCookie(w, &http.Cookie{
		Name: oauthNextCookie, Value: safeNext(r.URL.Query().Get("next")), Path: "/auth/github",
		MaxAge: 600, HttpOnly: true, Secure: s.secureCookies(), SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, s.github.AuthCodeURL(state), http.StatusFound)
}

func (s *Server) handleGitHubCallback(w http.ResponseWriter, r *http.Request) {
	if s.github == nil {
		writeErr(w, http.StatusNotFound, "GitHub login is not configured")
		return
	}
	ck, err := r.Cookie(oauthStateCookie)
	if err != nil || subtle.ConstantTimeCompare([]byte(ck.Value), []byte(r.URL.Query().Get("state"))) != 1 {
		writeErr(w, http.StatusBadRequest, "invalid OAuth state; start again at /auth/github/login")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: oauthStateCookie, Path: "/auth/github", MaxAge: -1})

	// Outbound GitHub calls are traced and carry the request's trace context.
	ctx := context.WithValue(r.Context(), oauth2.HTTPClient, &http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport)})
	tok, err := s.github.Exchange(ctx, r.URL.Query().Get("code"))
	if err != nil {
		writeErr(w, http.StatusBadGateway, "GitHub token exchange failed: "+err.Error())
		return
	}
	client := s.github.Client(ctx, tok)
	var gh struct {
		ID    int64  `json:"id"`
		Login string `json:"login"`
		Name  string `json:"name"`
		Email string `json:"email"`
	}
	if err := getJSON(ctx, client, "https://api.github.com/user", &gh); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	if gh.Email == "" {
		gh.Email = primaryEmail(ctx, client)
	}
	admin := slices.Contains(s.cfg.AdminLogins, gh.Login)
	p, err := s.dir.UpsertGitHubUser(ctx, gh.ID, gh.Login, gh.Email, gh.Name, admin)
	if err != nil {
		writeError(w, err)
		return
	}
	session, err := s.dir.CreateSession(ctx, p.ID, s.cfg.SessionTTL)
	if err != nil {
		writeError(w, err)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: session, Path: "/",
		MaxAge: int(s.cfg.SessionTTL.Seconds()), HttpOnly: true, Secure: s.secureCookies(), SameSite: http.SameSiteLaxMode,
	})
	next := "/"
	if ck, err := r.Cookie(oauthNextCookie); err == nil {
		next = safeNext(ck.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: oauthNextCookie, Path: "/auth/github", MaxAge: -1})
	http.Redirect(w, r, next, http.StatusFound)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if ck, err := r.Cookie(sessionCookie); err == nil {
		s.dir.DeleteSession(r.Context(), ck.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Path: "/", MaxAge: -1})
	w.WriteHeader(http.StatusNoContent)
}

func getJSON(ctx context.Context, client *http.Client, url string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("GitHub API: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GitHub API %s: %s", url, resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(v)
}

// primaryEmail looks up the verified primary address for accounts that
// hide their email from the public profile.
func primaryEmail(ctx context.Context, client *http.Client) string {
	var emails []struct {
		Email    string `json:"email"`
		Primary  bool   `json:"primary"`
		Verified bool   `json:"verified"`
	}
	if getJSON(ctx, client, "https://api.github.com/user/emails", &emails) != nil {
		return ""
	}
	for _, e := range emails {
		if e.Primary && e.Verified {
			return e.Email
		}
	}
	return ""
}
