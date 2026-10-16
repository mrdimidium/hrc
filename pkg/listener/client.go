// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

package listener

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
)

// Client implements the runner v2 Broker and Run Service APIs. It is safe for
// concurrent use: long polling does not block lease renewal or job completion.
type Client struct {
	httpClient                 *http.Client
	brokerURL                  string
	userAgent                  string
	credentials                Credentials
	key                        *rsa.PrivateKey
	authMu                     sync.Mutex
	token                      string
	tokenExpiry                time.Time
	tokenMigrated              bool
	authMigrationDeferredUntil time.Time
}

// NewClient creates a runner API client. A supplied HTTPClient is copied, so
// configuring request timeouts or redirect behavior does not mutate the caller.
func NewClient(config ClientConfig) (*Client, error) {
	if config.BrokerURL != "" && !validURL(config.BrokerURL) {
		return nil, errors.New("invalid Broker URL")
	}
	if config.PrivateKey != nil || config.Credentials.Scheme != "" {
		d := config.Credentials.value
		if !strings.EqualFold(config.Credentials.Scheme, "OAuth") || d("clientId") == "" || !validURL(d("authorizationUrl")) || config.PrivateKey == nil {
			return nil, errors.New("runner OAuth credentials and RSA key are required")
		}
		for _, name := range []string{"oauthEndpointUrl", "authorizationUrlV2"} {
			if d(name) != "" && !validURL(d(name)) {
				return nil, fmt.Errorf("invalid runner credential URL %s", name)
			}
		}
		if err := config.PrivateKey.Validate(); err != nil {
			return nil, errors.New("invalid runner RSA key")
		}
	}
	base := config.HTTPClient
	if base == nil {
		base = &http.Client{Timeout: 90 * time.Second}
	}
	httpClient := *base
	// Runner/job tokens and POST bodies must not follow redirects.
	httpClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	credentials := config.Credentials
	credentials.Data = maps.Clone(credentials.Data)
	return &Client{httpClient: &httpClient, brokerURL: config.BrokerURL,
		userAgent: "GitHubActionsRunner/" + config.Version, credentials: credentials, key: config.PrivateKey}, nil
}

// Close forgets runner credentials after the caller has joined all requests.
// It does not close the caller's HTTP transport.
func (c *Client) Close() error {
	c.authMu.Lock()
	defer c.authMu.Unlock()
	c.credentials.Data, c.key, c.token, c.tokenExpiry = nil, nil, "", time.Time{}
	c.tokenMigrated, c.authMigrationDeferredUntil = false, time.Time{}
	return nil
}

// RefreshRunnerToken forces the next runner-authenticated request to exchange a
// new OAuth token. Job tokens are independent and remain unchanged.
func (c *Client) RefreshRunnerToken() { c.invalidateToken("") }

func (c *Client) authToken(ctx context.Context) (string, error) {
	c.authMu.Lock()
	defer c.authMu.Unlock()
	if c.token != "" && time.Until(c.tokenExpiry) > 30*time.Second {
		return c.token, nil
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if c.key == nil {
		return "", ErrCredentials
	}
	d := c.credentials.value
	audience := d("authorizationUrl")
	migrated := strings.EqualFold(d("enableAuthMigrationByDefault"), "true") &&
		d("authorizationUrlV2") != "" && d("authorizationUrlV2") != audience &&
		!time.Now().Before(c.authMigrationDeferredUntil)
	if migrated {
		audience = d("authorizationUrlV2")
	}
	token, err := c.exchangeToken(ctx, audience)
	if err != nil && migrated && ctx.Err() == nil {
		// Like the .NET listener, defer migration for an hour when the new
		// authentication path fails. Each exchange gets its own timeout.
		c.authMigrationDeferredUntil = time.Now().Add(time.Hour)
		migrated = false
		token, err = c.exchangeToken(ctx, d("authorizationUrl"))
	}
	if err != nil {
		return "", err
	}
	c.token, c.tokenExpiry, c.tokenMigrated = token.AccessToken, token.Expiry, migrated
	return c.token, nil
}

// exchangeToken is called with authMu held. Errors retain retry semantics but
// never expose OAuth response bodies, assertions or credential URLs.
func (c *Client) exchangeToken(ctx context.Context, audience string) (*oauth2.Token, error) {
	attempt, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	d := c.credentials.value
	endpoint := d("oauthEndpointUrl")
	if endpoint == "" {
		endpoint = audience
	}
	if !validURL(audience) || !validURL(endpoint) {
		return nil, errors.New("invalid runner OAuth URL")
	}
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	now := time.Now()
	claims, _ := json.Marshal(map[string]any{"iss": d("clientId"), "sub": d("clientId"), "aud": audience, "nbf": now.Unix(), "exp": now.Add(5 * time.Minute).Unix(), "jti": fmt.Sprintf("%x", nonce)})
	assertion := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`)) + "." + base64.RawURLEncoding.EncodeToString(claims)
	hash := sha256.Sum256([]byte(assertion))
	sig, err := rsa.SignPKCS1v15(rand.Reader, c.key, crypto.SHA256, hash[:])
	if err != nil {
		return nil, err
	}
	assertion += "." + base64.RawURLEncoding.EncodeToString(sig)
	// Construct the config for each exchange: reusing a TokenSource with a
	// fixed EndpointParams would eventually reuse an expired JWT assertion.
	config := clientcredentials.Config{
		ClientID: d("clientId"), TokenURL: endpoint, AuthStyle: oauth2.AuthStyleInParams,
		EndpointParams: url.Values{
			"client_assertion_type": {"urn:ietf:params:oauth:client-assertion-type:jwt-bearer"},
			"client_assertion":      {assertion},
		},
	}
	token, err := config.Token(context.WithValue(attempt, oauth2.HTTPClient, c.httpClient))
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var response *oauth2.RetrieveError
		if errors.As(err, &response) && response.Response != nil {
			requestErr := newRequestError(response.Response, nil)
			requestErr.Kind = "OAuth"
			return nil, requestErr
		}
		// oauth2 errors can include the response body and hence credentials.
		// Includes transport/read failures and the exchange's own timeout.
		// The caller's live context allows the outer request loop to retry.
		return nil, errTransport
	}
	if token.AccessToken == "" || !token.Expiry.After(time.Now()) {
		return nil, errors.New("invalid OAuth token lifetime")
	}
	return token, nil
}

func (c *Client) invalidateToken(old string) {
	c.authMu.Lock()
	defer c.authMu.Unlock()
	if old == "" || c.token == old {
		c.token, c.tokenExpiry = "", time.Time{}
		c.tokenMigrated = false
	}
}

func (c *Client) deferAuthMigration(token string) bool {
	c.authMu.Lock()
	defer c.authMu.Unlock()
	if token == "" || token != c.token || !c.tokenMigrated {
		return false
	}
	c.authMigrationDeferredUntil = time.Now().Add(time.Hour)
	c.token, c.tokenExpiry, c.tokenMigrated = "", time.Time{}, false
	return true
}

// do shares JSON transport, runner authentication and retries between endpoints.
func (c *Client) do(ctx context.Context, method, base, path string, query url.Values, payload any, jobToken string, attempts int, target any) error {
	if !validURL(base) {
		return errors.New("invalid runner service URL")
	}
	endpoint, err := url.JoinPath(base, path)
	if err != nil {
		return err
	}
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	var body []byte
	if payload != nil {
		var err error
		body, err = json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("encode runner request: %w", err)
		}
	}
	refreshed := false
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		var err error
		token := jobToken
		if token == "" {
			token, err = c.authToken(ctx)
		}
		if err == nil {
			var req *http.Request
			req, err = http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
			if err != nil {
				return err
			}
			req.Header.Set("Accept", "application/json")
			req.Header.Set("User-Agent", c.userAgent)
			req.Header.Set("Authorization", "Bearer "+token)
			if payload != nil {
				req.Header.Set("Content-Type", "application/json")
			}
			err = c.send(req, target)
		}
		if err == nil {
			return nil
		}
		// A migrated token may be issued successfully but rejected by Broker
		// or Run Service. Retry with baseline credentials before giving up.
		if jobToken == "" && ctx.Err() == nil && statusIs(err, http.StatusUnauthorized, http.StatusForbidden) && c.deferAuthMigration(token) {
			attempt--
			continue
		}
		if statusIs(err, http.StatusUnauthorized) && jobToken == "" && !refreshed {
			c.invalidateToken(token)
			refreshed = true
			attempt--
			continue
		}
		if attempt+1 >= attempts || !retryable(err) {
			return err
		}
		delay := time.Second << min(attempt, 5)
		var response *RequestError
		if errors.As(err, &response) {
			delay = max(delay, response.RetryAfter)
		}
		if err := wait(ctx, delay); err != nil {
			return err
		}
	}
}

func (c *Client) send(req *http.Request, target any) error {
	response, err := c.httpClient.Do(req)
	if err != nil {
		if req.Context().Err() != nil {
			return req.Context().Err()
		}
		// Transport errors may contain URLs and query parameters with secrets.
		return errTransport
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 50_000_001))
	if err != nil {
		return errTransport
	}
	if len(data) > 50_000_000 {
		return errors.New("runner API response exceeds 50 MB")
	}
	data = bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return newRequestError(response, data)
	}
	if target != nil && len(bytes.TrimSpace(data)) != 0 {
		if json.Unmarshal(data, target) != nil {
			return errors.New("invalid runner API JSON response")
		}
	}
	return nil
}
