package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// APIError is an error response from the server.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string { return e.Message }

// IsNotFound reports whether err is a 404 from the server.
func IsNotFound(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Status == http.StatusNotFound
}

// Client talks to a tt server. It exchanges its API key for a JWT on first
// use and again when the JWT is rejected.
type Client struct {
	Server string
	key    string
	http   *http.Client

	mu    sync.Mutex
	token string
}

func New(server, key string) *Client {
	return &Client{Server: normalizeServer(server), key: key, http: &http.Client{Timeout: 30 * time.Second}}
}

// Do sends an authenticated JSON request and decodes the response into out
// (if non-nil).
func (c *Client) Do(ctx context.Context, method, path string, body, out any) error {
	tok, err := c.jwt(ctx, false)
	if err != nil {
		return err
	}
	err = c.send(ctx, method, path, tok, body, out)
	var ae *APIError
	if errors.As(err, &ae) && ae.Status == http.StatusUnauthorized {
		// Expired or revoked JWT: exchange again once.
		if tok, err = c.jwt(ctx, true); err != nil {
			return err
		}
		err = c.send(ctx, method, path, tok, body, out)
	}
	return err
}

// Anonymous sends a request without credentials (login endpoints).
func (c *Client) Anonymous(ctx context.Context, method, path string, body, out any) error {
	return c.send(ctx, method, path, "", body, out)
}

func (c *Client) jwt(ctx context.Context, refresh bool) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && !refresh {
		return c.token, nil
	}
	var resp struct {
		Token string `json:"token"`
	}
	if err := c.send(ctx, "POST", "/api/v1/token", c.key, nil, &resp); err != nil {
		var ae *APIError
		if errors.As(err, &ae) && ae.Status == http.StatusUnauthorized {
			return "", fmt.Errorf("%s rejected your credentials (%s): run tt login", c.Server, ae.Message)
		}
		return "", err
	}
	c.token = resp.Token
	return c.token, nil
}

func (c *Client) send(ctx context.Context, method, path, bearer string, body, out any) error {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.Server+path, r)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("tt server %s: %w", c.Server, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(data, &e) != nil || e.Error == "" {
			e.Error = fmt.Sprintf("%s %s: %s", method, path, resp.Status)
		}
		return &APIError{Status: resp.StatusCode, Message: e.Error}
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}

// Stream opens a long-lived authenticated GET (server-sent events). The
// caller closes the response body.
func (c *Client) Stream(ctx context.Context, path string) (*http.Response, error) {
	open := func(refresh bool) (*http.Response, error) {
		tok, err := c.jwt(ctx, refresh)
		if err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, "GET", c.Server+path, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("Accept", "text/event-stream")
		// No client timeout: the stream stays open until ctx ends.
		return http.DefaultClient.Do(req)
	}
	resp, err := open(false)
	if err == nil && resp.StatusCode == http.StatusUnauthorized {
		resp.Body.Close()
		resp, err = open(true)
	}
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		return nil, &APIError{Status: resp.StatusCode, Message: fmt.Sprintf("GET %s: %s", path, resp.Status)}
	}
	return resp, nil
}
