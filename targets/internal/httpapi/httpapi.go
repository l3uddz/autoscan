// Package httpapi holds the HTTP client that the targets share.
// The client maps transport errors and response statuses to the autoscan
// errors that the processor uses, and it keeps credentials out of errors
// and logs.
package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"github.com/l3uddz/autoscan"
)

// timeout limits each HTTP request that a target sends.
// When a request times out, the target is unavailable, and the processor
// retries later. The scan stays in the datastore until then.
const timeout = 30 * time.Second

// Config describes how a Client talks to one media server.
type Config struct {
	// BaseURL is the server URL from the target configuration.
	BaseURL string

	// Log receives a trace line for each failed request.
	Log zerolog.Logger

	// Prepare adds the headers or query parameters that the server needs,
	// such as the token, to each request.
	Prepare func(*http.Request)

	// AuthMessage describes a 401 response.
	AuthMessage string

	// SecretParams are the query parameters that carry tokens.
	// The client removes them from logged URLs.
	SecretParams []string
}

// A Client sends requests to one media server.
type Client struct {
	client *http.Client
	config Config
}

// New returns a Client for c.
func New(c Config) *Client {
	return &Client{
		client: &http.Client{Timeout: timeout},
		config: c,
	}
}

// Send sends a request without a body and closes the response body.
// endpoint is relative to the base URL. If query is not nil, it replaces
// the query of the request URL.
func (c *Client) Send(method string, endpoint string, query url.Values) error {
	res, err := c.do(method, endpoint, query, nil, "")
	if err != nil {
		return err
	}

	return res.Body.Close()
}

// SendJSON sends v as the JSON body of a request and closes the response body.
func (c *Client) SendJSON(method string, endpoint string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("encoding request body: %v: %w", err, autoscan.ErrFatal)
	}

	res, err := c.do(method, endpoint, nil, bytes.NewReader(b), "application/json")
	if err != nil {
		return err
	}

	return res.Body.Close()
}

// GetJSON sends a GET request and decodes the JSON response body into v.
func (c *Client) GetJSON(endpoint string, v any) error {
	res, err := c.do(http.MethodGet, endpoint, nil, nil, "")
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if err := json.NewDecoder(res.Body).Decode(v); err != nil {
		return bodyError("decoding response", err)
	}

	return nil
}

// do sends a request and returns the response for a 2xx status.
// The caller closes the response body.
func (c *Client) do(method string, endpoint string, query url.Values, body io.Reader, contentType string) (*http.Response, error) {
	req, err := http.NewRequest(method, joinURL(c.config.BaseURL, endpoint), body)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", requestError(err, autoscan.ErrFatal))
	}

	if query != nil {
		req.URL.RawQuery = query.Encode()
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if c.config.Prepare != nil {
		c.config.Prepare(req)
	}

	res, err := c.client.Do(req)
	if err != nil {
		return nil, requestError(err, autoscan.ErrTargetUnavailable)
	}

	if err := responseError(res, c.config.AuthMessage); err != nil {
		c.config.Log.Trace().
			Str("request_url", RedactURL(res.Request.URL.String(), c.config.SecretParams...)).
			Int("response_status", res.StatusCode).
			Msg("Request failed")

		return nil, err
	}

	return res, nil
}

// requestError omits the request URL, which can contain credentials.
func requestError(err, cause error) error {
	var urlErr *url.Error
	for errors.As(err, &urlErr) {
		err = urlErr.Err
	}
	return fmt.Errorf("%v: %w", err, cause)
}

// RedactURL returns raw for a log line. It replaces the user password with
// "xxxxx" and removes the secretParams query parameters, which carry tokens.
// If raw is not a valid URL, RedactURL returns "invalid url".
func RedactURL(raw string, secretParams ...string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "invalid url"
	}

	if len(secretParams) > 0 {
		q := u.Query()
		for _, p := range secretParams {
			q.Del(p)
		}
		u.RawQuery = q.Encode()
	}

	return u.Redacted()
}

// bodyError wraps an error from reading or decoding a response body.
// A timeout means that the target is unavailable, so the processor retries.
// Any other error is fatal.
func bodyError(msg string, err error) error {
	sentinel := autoscan.ErrFatal

	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		sentinel = autoscan.ErrTargetUnavailable
	}

	return fmt.Errorf("%s: %v: %w", msg, err, sentinel)
}

// responseError returns nil for a 2xx response. For any other status, it
// closes the response body and returns an error. A 401 status is fatal, and
// authMessage describes it. 404 and 5xx gateway errors mean that the target
// is unavailable. Any other status is fatal.
func responseError(res *http.Response, authMessage string) error {
	if res.StatusCode >= 200 && res.StatusCode < 300 {
		return nil
	}

	res.Body.Close()

	switch res.StatusCode {
	case http.StatusUnauthorized:
		return fmt.Errorf("%s: %s: %w", authMessage, res.Status, autoscan.ErrFatal)
	case http.StatusNotFound, http.StatusInternalServerError, http.StatusBadGateway,
		http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return fmt.Errorf("%s: %w", res.Status, autoscan.ErrTargetUnavailable)
	default:
		return fmt.Errorf("%s: %w", res.Status, autoscan.ErrFatal)
	}
}

func joinURL(base string, endpoint string) string {
	// credits: https://stackoverflow.com/a/57220413
	p := path.Join(endpoint)
	return fmt.Sprintf("%s/%s", strings.TrimRight(base, "/"), strings.TrimLeft(p, "/"))
}
