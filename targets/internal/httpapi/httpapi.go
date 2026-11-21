// Package httpapi holds the HTTP client that the targets share.
// The client marks the errors that mean that a target is unavailable with
// autoscan.ErrTargetUnavailable, and it keeps credentials out of errors and
// logs.
package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
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
	base   *url.URL
	config Config
}

// New returns a Client for c. It returns an error if c.BaseURL is not an
// http or https URL with a host, or if it has a query or a fragment.
func New(c Config) (*Client, error) {
	base, err := parseBaseURL(c.BaseURL)
	if err != nil {
		return nil, err
	}

	return &Client{
		client: &http.Client{Timeout: timeout, CheckRedirect: checkRedirect},
		base:   base,
		config: c,
	}, nil
}

// maxRedirects is how many redirects a request follows, as in Go's default.
const maxRedirects = 10

// errRedirect means that the target redirected a request to another host,
// or that the redirect changed the method, such as a POST to a GET. The
// token headers must not reach another host, and a scan sent as a GET is
// lost. The error marks the target unavailable, so the scans stay queued.
var errRedirect = errors.New("target redirected the request to another host or method, set the target url to the address that answers")

// checkRedirect follows a redirect only to the same host name with the same
// method, such as from http to https.
func checkRedirect(req *http.Request, via []*http.Request) error {
	first := via[0]
	if req.URL.Hostname() != first.URL.Hostname() || req.Method != first.Method {
		return errRedirect
	}
	if len(via) >= maxRedirects {
		return fmt.Errorf("stopped after %d redirects", maxRedirects)
	}
	return nil
}

// parseBaseURL parses the base URL of a target. A path prefix, such as
// the path of a reverse proxy, and a user name with password are allowed.
// A port without a host name, such as http://:32400, is allowed too,
// because Go then dials the local system.
// The errors do not contain the URL, because it can contain credentials.
func parseBaseURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid target url: %w", withoutURL(err))
	}

	switch {
	case u.Scheme != "http" && u.Scheme != "https":
		return nil, errors.New("target url must start with http:// or https://")
	case u.Host == "":
		return nil, errors.New("target url has no host")
	case u.RawQuery != "" || u.ForceQuery:
		return nil, errors.New("target url must not have a query")
	case u.Fragment != "":
		return nil, errors.New("target url must not have a fragment")
	}

	return u, nil
}

// Send sends a request without a body and closes the response body.
// elem are the path elements after the base URL. If query is not nil, it
// is the query of the request URL. ctx ends the request.
func (c *Client) Send(ctx context.Context, method string, query url.Values, elem ...string) error {
	req, err := c.newRequest(ctx, method, query, nil, elem)
	if err != nil {
		return err
	}

	res, err := c.do(req)
	if err != nil {
		return err
	}

	// The server accepted the request, so a failed close does not fail it.
	res.Body.Close()
	return nil
}

// PostJSON sends v as the JSON body of a POST request and closes the
// response body.
func (c *Client) PostJSON(ctx context.Context, v any, elem ...string) error {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("encoding request body: %w", err)
	}

	req, err := c.newRequest(ctx, http.MethodPost, nil, bytes.NewReader(b), elem)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	res, err := c.do(req)
	if err != nil {
		return err
	}

	res.Body.Close()
	return nil
}

// GetJSON sends a GET request and decodes the JSON response body into v.
func (c *Client) GetJSON(ctx context.Context, v any, elem ...string) error {
	req, err := c.newRequest(ctx, http.MethodGet, nil, nil, elem)
	if err != nil {
		return err
	}

	res, err := c.do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if err := json.NewDecoder(res.Body).Decode(v); err != nil {
		return bodyError("decoding response", err)
	}

	return nil
}

// newRequest returns a request for the path elements elem after the base
// URL. JoinPath returns a new URL, so Prepare can change the request URL
// without changing the base URL.
func (c *Client) newRequest(ctx context.Context, method string, query url.Values, body io.Reader, elem []string) (*http.Request, error) {
	u := c.base.JoinPath(elem...)
	u.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", withoutURL(err))
	}

	return req, nil
}

// do prepares and sends req, and returns the response for a 2xx status.
// The caller closes the response body.
func (c *Client) do(req *http.Request) (*http.Response, error) {
	if c.config.Prepare != nil {
		c.config.Prepare(req)
	}

	res, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", withoutURL(err), autoscan.ErrTargetUnavailable)
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

// withoutURL returns the cause of a *url.Error. The error then omits the
// request URL, which can contain credentials.
func withoutURL(err error) error {
	var urlErr *url.Error
	for errors.As(err, &urlErr) {
		err = urlErr.Err
	}
	return err
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
// A timeout means that the target is unavailable.
func bodyError(msg string, err error) error {
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return fmt.Errorf("%s: %w: %w", msg, err, autoscan.ErrTargetUnavailable)
	}

	return fmt.Errorf("%s: %w", msg, err)
}

// responseError returns nil for a 2xx response. For any other status, it
// closes the response body and returns an error. authMessage describes a 401
// status. 404 and 5xx gateway errors mean that the target is unavailable.
func responseError(res *http.Response, authMessage string) error {
	if res.StatusCode >= 200 && res.StatusCode < 300 {
		return nil
	}

	res.Body.Close()

	switch res.StatusCode {
	case http.StatusUnauthorized:
		return fmt.Errorf("%s: %s", authMessage, res.Status)
	case http.StatusNotFound, http.StatusInternalServerError, http.StatusBadGateway,
		http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return fmt.Errorf("%s: %w", res.Status, autoscan.ErrTargetUnavailable)
	default:
		return errors.New(res.Status)
	}
}
