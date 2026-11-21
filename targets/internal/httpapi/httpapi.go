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
	"strings"
	"time"

	"github.com/rs/zerolog"

	"github.com/l3uddz/autoscan"
)

// timeout limits each HTTP request that a target sends.
// When a request times out, the target is unavailable, and the processor
// retries later. The scan stays in the datastore until then.
const timeout = 30 * time.Second

const (
	// maxDrainBytes bounds how much of an unused or error response body is
	// drained so HTTP keep-alive connections can be reused.
	maxDrainBytes = 64 << 10

	// maxResponseBytes bounds the JSON response body read by GetJSON.
	maxResponseBytes = 10 << 20
)

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

// errRedirect means that the target redirected a request to another host
// or port, from https to http, or to another method, such as from a POST
// to a GET. The token headers must not reach another address or travel
// without encryption, and a scan sent as a GET is lost. The error marks the
// target unavailable, so the scans stay queued.
var errRedirect = errors.New("target redirected the request to another host, port, scheme, or method, set the target url to the address that answers")

// checkRedirect follows a redirect only to the same host and port with the
// same method. It allows a change from http to https, but not back.
func checkRedirect(req *http.Request, via []*http.Request) error {
	first, previous := via[0], via[len(via)-1]
	sameHost := strings.EqualFold(req.URL.Hostname(), first.URL.Hostname()) && explicitPort(req.URL) == explicitPort(first.URL)
	downgrade := previous.URL.Scheme == "https" && req.URL.Scheme != "https"
	if !sameHost || downgrade || req.Method != first.Method {
		return errRedirect
	}
	if len(via) >= maxRedirects {
		return fmt.Errorf("stopped after %d redirects", maxRedirects)
	}
	return nil
}

// explicitPort returns the port of u, or "" for the default port of its
// scheme. A change from http to https on the default ports then keeps the
// port.
func explicitPort(u *url.URL) string {
	port := u.Port()
	if (u.Scheme == "http" && port == "80") || (u.Scheme == "https" && port == "443") {
		return ""
	}
	return port
}

// CheckBaseURL returns the error that New returns for the base URL raw.
// The configuration checks it at startup.
func CheckBaseURL(raw string) error {
	_, err := parseBaseURL(raw)
	return err
}

// CheckToken returns an error if token contains a quote or a control
// character. Such a token breaks the header that carries it, so the server
// rejects every request.
func CheckToken(token string) error {
	if strings.ContainsFunc(token, func(r rune) bool { return r < ' ' || r == 0x7f || r == '"' }) {
		return errors.New("token contains a quote or a control character")
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

// Send sends a request without a body and ignores the response body.
// elem are the path elements after the base URL. If query is not nil, it
// is the query of the request URL. ctx ends the request.
func (c *Client) Send(ctx context.Context, method string, query url.Values, elem ...string) error {
	return c.send(ctx, method, query, nil, nil, elem)
}

// PostJSON sends v as the JSON body of a POST request and ignores the
// response body.
func (c *Client) PostJSON(ctx context.Context, v any, elem ...string) error {
	return c.send(ctx, http.MethodPost, nil, v, nil, elem)
}

// GetJSON sends a GET request and decodes the JSON response body into v.
func (c *Client) GetJSON(ctx context.Context, v any, elem ...string) error {
	return c.send(ctx, http.MethodGet, nil, nil, v, elem)
}

// send sends a request for the path elements elem after the base URL. If
// in is not nil, it is the JSON body. If out is not nil, send decodes the
// JSON response body into it. JoinPath returns a new URL, so Prepare can
// change the request URL without changing the base URL.
func (c *Client) send(ctx context.Context, method string, query url.Values, in any, out any, elem []string) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("encoding request body: %w", err)
		}
		body = bytes.NewReader(b)
	}

	u := c.base.JoinPath(elem...)
	u.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return fmt.Errorf("creating request: %w", withoutURL(err))
	}
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	res, err := c.do(req)
	if err != nil {
		return err
	}
	// The server accepted the request, so a failed close does not fail it.
	defer closeBody(res.Body)

	if out == nil {
		return nil
	}
	return decodeJSON(res.Body, out)
}

func closeBody(body io.ReadCloser) {
	_, _ = io.Copy(io.Discard, io.LimitReader(body, maxDrainBytes))
	_ = body.Close()
}

func decodeJSON(r io.Reader, out any) error {
	limited := &io.LimitedReader{R: r, N: maxResponseBytes + 1}
	if err := json.NewDecoder(limited).Decode(out); err != nil {
		if limited.N == 0 {
			return fmt.Errorf("decoding response: response exceeds %d bytes", maxResponseBytes)
		}
		return bodyError("decoding response", err)
	}
	if limited.N == 0 {
		return fmt.Errorf("decoding response: response exceeds %d bytes", maxResponseBytes)
	}
	return nil
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
// A network error or truncated stream means that the target is unavailable.
func bodyError(msg string, err error) error {
	var netErr net.Error
	if errors.As(err, &netErr) || errors.Is(err, io.ErrUnexpectedEOF) {
		return fmt.Errorf("%s: %w: %w", msg, err, autoscan.ErrTargetUnavailable)
	}

	return fmt.Errorf("%s: %w", msg, err)
}

// responseError returns nil for a 2xx response. For any other status, it
// drains and closes the response body and returns an error. authMessage
// describes a 401 status. 404, 408, 429, and 5xx errors other than 501 mean
// that the target is unavailable.
func responseError(res *http.Response, authMessage string) error {
	if res.StatusCode >= 200 && res.StatusCode < 300 {
		return nil
	}

	closeBody(res.Body)

	switch {
	case res.StatusCode == http.StatusUnauthorized:
		return fmt.Errorf("%s: %s", authMessage, res.Status)
	case res.StatusCode == http.StatusNotFound,
		res.StatusCode == http.StatusRequestTimeout,
		res.StatusCode == http.StatusTooManyRequests,
		res.StatusCode >= 500 && res.StatusCode != http.StatusNotImplemented:
		return fmt.Errorf("%s: %w", res.Status, autoscan.ErrTargetUnavailable)
	default:
		return errors.New(res.Status)
	}
}
