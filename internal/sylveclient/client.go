// Package sylveclient is a minimal client for the Sylve REST API
// (https://sylve.io/api-reference/). It only implements what the
// terraform-provider-sylve resources currently need.
package sylveclient

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
)

// Client talks to one Sylve node's REST API.
type Client struct {
	endpoint   string // e.g. "https://sylve.example.com:8181"
	httpClient *http.Client

	mu       sync.Mutex
	username string
	password string
	authType string // "sylve" or "pam"
	token    string
}

// NewClient builds a client for the given Sylve API endpoint. insecureTLS
// should only be set true for self-signed-cert test/dev instances.
func NewClient(endpoint, username, password, authType string, insecureTLS bool) *Client {
	transport := &http.Transport{}
	if insecureTLS {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // opt-in, documented
	}
	return &Client{
		endpoint:   endpoint,
		username:   username,
		password:   password,
		authType:   authType,
		httpClient: &http.Client{Transport: transport},
	}
}

type loginRequest struct {
	AuthType string `json:"authType"`
	Username string `json:"username"`
	Password string `json:"password"`
	Remember bool   `json:"remember"`
}

type loginResponse struct {
	Data struct {
		Token string `json:"token"`
	} `json:"data"`
	Error string `json:"error"`
}

// Login authenticates and caches the bearer token for subsequent requests.
// Safe to call again to refresh; callers normally don't need to call it
// directly -- do() calls it lazily on the first request.
func (c *Client) Login(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.loginLocked(ctx)
}

func (c *Client) loginLocked(ctx context.Context) error {
	body, err := json.Marshal(loginRequest{
		AuthType: c.authType,
		Username: c.username,
		Password: c.password,
		Remember: false,
	})
	if err != nil {
		return fmt.Errorf("encoding login request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint+"/api/auth/login", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("login request: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("login failed: HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var lr loginResponse
	if err := json.Unmarshal(respBody, &lr); err != nil {
		return fmt.Errorf("decoding login response: %w", err)
	}
	if lr.Data.Token == "" {
		return fmt.Errorf("login response carried no token: %s", string(respBody))
	}
	c.token = lr.Data.Token
	return nil
}

// apiError carries a Sylve API error response for callers that want to
// distinguish e.g. 404 (not found) from other failures.
type apiError struct {
	StatusCode int
	Body       string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("sylve API: HTTP %d: %s", e.StatusCode, e.Body)
}

// IsNotFound reports whether err is a 404 from the Sylve API -- resources'
// Read implementations use this to detect out-of-band deletion.
func IsNotFound(err error) bool {
	var ae *apiError
	if err == nil {
		return false
	}
	if ok := asAPIError(err, &ae); ok {
		return ae.StatusCode == http.StatusNotFound
	}
	return false
}

func asAPIError(err error, target **apiError) bool {
	ae, ok := err.(*apiError)
	if ok {
		*target = ae
	}
	return ok
}

// do performs an authenticated request against path (e.g. "/api/vm"),
// retrying once after a fresh login if the token was rejected as expired.
func (c *Client) do(ctx context.Context, method, path string, reqBody any, out any) error {
	send := func() (*http.Response, []byte, error) {
		var reader io.Reader
		if reqBody != nil {
			b, err := json.Marshal(reqBody)
			if err != nil {
				return nil, nil, fmt.Errorf("encoding request body: %w", err)
			}
			reader = bytes.NewReader(b)
		}

		req, err := http.NewRequestWithContext(ctx, method, c.endpoint+path, reader)
		if err != nil {
			return nil, nil, err
		}
		req.Header.Set("Content-Type", "application/json")

		c.mu.Lock()
		token := c.token
		c.mu.Unlock()
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, nil, err
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp, body, nil
	}

	c.mu.Lock()
	needLogin := c.token == ""
	c.mu.Unlock()
	if needLogin {
		if err := c.Login(ctx); err != nil {
			return err
		}
	}

	resp, body, err := send()
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}

	if resp.StatusCode == http.StatusUnauthorized {
		// Token likely expired -- re-login once and retry.
		if err := c.Login(ctx); err != nil {
			return err
		}
		resp, body, err = send()
		if err != nil {
			return fmt.Errorf("%s %s (after re-login): %w", method, path, err)
		}
	}

	if resp.StatusCode >= 300 {
		return &apiError{StatusCode: resp.StatusCode, Body: string(body)}
	}

	if out != nil && len(body) > 0 {
		if err := json.Unmarshal(body, out); err != nil {
			return fmt.Errorf("decoding response from %s %s: %w", method, path, err)
		}
	}
	return nil
}
