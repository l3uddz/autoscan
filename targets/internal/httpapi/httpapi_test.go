package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/l3uddz/autoscan"
)

func TestRequestErrorOmitsURL(t *testing.T) {
	requestErr := &url.Error{
		Op:  "Get",
		URL: "http://example.invalid/?token=example-token",
		Err: os.ErrDeadlineExceeded,
	}
	err := requestError(requestErr, autoscan.ErrTargetUnavailable)
	if !errors.Is(err, autoscan.ErrTargetUnavailable) || !strings.Contains(err.Error(), os.ErrDeadlineExceeded.Error()) {
		t.Fatalf("error %q does not describe the timeout", err)
	}
	if strings.Contains(err.Error(), "example-token") {
		t.Fatal("error contains the token from the request URL")
	}
}

func TestRedactURL(t *testing.T) {
	type Test struct {
		Name         string
		Raw          string
		SecretParams []string
		Want         string
	}

	var testCases = []Test{
		{
			Name: "Password",
			Raw:  "http://user:secret@example.invalid:8080/path",
			Want: "http://user:xxxxx@example.invalid:8080/path",
		},
		{
			Name: "No userinfo",
			Raw:  "http://example.invalid/path?b=2&a=1",
			Want: "http://example.invalid/path?b=2&a=1",
		},
		{
			Name: "Username only",
			Raw:  "http://user@example.invalid",
			Want: "http://user@example.invalid",
		},
		{
			Name: "Invalid URL",
			Raw:  "http://user:secret@example.invalid:port",
			Want: "invalid url",
		},
		{
			Name:         "Secret parameter",
			Raw:          "http://user:secret@example.invalid/path?ApiKey=token&path=%2Fmnt",
			SecretParams: []string{"ApiKey"},
			Want:         "http://user:xxxxx@example.invalid/path?path=%2Fmnt",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			if got := RedactURL(tc.Raw, tc.SecretParams...); got != tc.Want {
				t.Errorf("RedactURL(%q) = %q, want %q", tc.Raw, got, tc.Want)
			}
		})
	}
}

func TestBodyErrorClassifies(t *testing.T) {
	type Test struct {
		Name string
		Err  error
		Want error
	}

	var testCases = []Test{
		{
			Name: "Timeout is unavailable",
			Err:  os.ErrDeadlineExceeded,
			Want: autoscan.ErrTargetUnavailable,
		},
		{
			Name: "Invalid JSON is fatal",
			Err:  json.Unmarshal([]byte("{"), new(struct{})),
			Want: autoscan.ErrFatal,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			err := bodyError("decoding response", tc.Err)
			if !errors.Is(err, tc.Want) {
				t.Errorf("Got error %v, want %v", err, tc.Want)
			}
		})
	}
}

func TestResponseErrorClassifies(t *testing.T) {
	type Test struct {
		Status int
		Want   error
	}

	var testCases = []Test{
		{http.StatusOK, nil},
		{http.StatusNoContent, nil},
		{http.StatusUnauthorized, autoscan.ErrFatal},
		{http.StatusForbidden, autoscan.ErrFatal},
		{http.StatusNotFound, autoscan.ErrTargetUnavailable},
		{http.StatusInternalServerError, autoscan.ErrTargetUnavailable},
		{http.StatusBadGateway, autoscan.ErrTargetUnavailable},
		{http.StatusServiceUnavailable, autoscan.ErrTargetUnavailable},
		{http.StatusGatewayTimeout, autoscan.ErrTargetUnavailable},
		{http.StatusNotImplemented, autoscan.ErrFatal},
	}

	for _, tc := range testCases {
		t.Run(http.StatusText(tc.Status), func(t *testing.T) {
			body := &closeRecorder{Reader: strings.NewReader("")}
			res := &http.Response{StatusCode: tc.Status, Status: http.StatusText(tc.Status), Body: body}

			err := responseError(res, "invalid token")
			if tc.Want == nil {
				if err != nil || body.closed {
					t.Errorf("Got error %v and closed=%v, want nil and an open body", err, body.closed)
				}
				return
			}

			if !errors.Is(err, tc.Want) {
				t.Errorf("Got error %v, want %v", err, tc.Want)
			}
			if !body.closed {
				t.Error("Body is open, want it closed")
			}
			if tc.Status == http.StatusUnauthorized && !strings.Contains(err.Error(), "invalid token") {
				t.Errorf("Got error %v, want it to contain the auth message", err)
			}
		})
	}
}

type closeRecorder struct {
	io.Reader
	closed bool
}

func (c *closeRecorder) Close() error {
	c.closed = true
	return nil
}

// newTestClient returns a Client for url with a short timeout.
func newTestClient(url string) *Client {
	c := New(Config{BaseURL: url, Log: zerolog.Nop()})
	c.client.Timeout = 50 * time.Millisecond
	return c
}

func TestNewUsesTargetTimeout(t *testing.T) {
	if got := New(Config{}).client.Timeout; got != timeout {
		t.Fatalf("HTTP client timeout = %v, want %v", got, timeout)
	}
}

func TestRequestTimeoutIsUnavailable(t *testing.T) {
	// The server never answers until the test ends.
	done := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		<-done
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(done) })

	if err := newTestClient(srv.URL).Send(http.MethodGet, "", nil); !errors.Is(err, autoscan.ErrTargetUnavailable) {
		t.Errorf("Got error %v, want %v", err, autoscan.ErrTargetUnavailable)
	}
}

func TestBodyTimeoutIsUnavailable(t *testing.T) {
	// The server sends the headers, then never sends the body.
	done := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-done
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(done) })

	var v struct{}
	if err := newTestClient(srv.URL).GetJSON("", &v); !errors.Is(err, autoscan.ErrTargetUnavailable) {
		t.Errorf("Got error %v, want %v", err, autoscan.ErrTargetUnavailable)
	}
}

func TestSendBuildsRequest(t *testing.T) {
	var got *http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r
	}))
	defer srv.Close()

	c := New(Config{
		BaseURL: srv.URL + "/base/",
		Log:     zerolog.Nop(),
		Prepare: func(req *http.Request) { req.Header.Set("X-Token", "token") },
	})
	if err := c.Send(http.MethodPost, "library/sections/1/refresh", url.Values{"path": {"/mnt/tv"}}); err != nil {
		t.Fatalf("Send returned %v", err)
	}

	if got.Method != http.MethodPost || got.URL.Path != "/base/library/sections/1/refresh" {
		t.Errorf("Request = %s %s, want POST /base/library/sections/1/refresh", got.Method, got.URL.Path)
	}
	if q := got.URL.Query().Get("path"); q != "/mnt/tv" {
		t.Errorf("path query = %q, want /mnt/tv", q)
	}
	if h := got.Header.Get("X-Token"); h != "token" {
		t.Errorf("X-Token header = %q, want token", h)
	}
}

func TestFailedRequestLogRemovesSecrets(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()

	var buf strings.Builder
	c := New(Config{
		BaseURL: strings.Replace(srv.URL, "http://", "http://user:secret@", 1),
		Log:     zerolog.New(&buf).Level(zerolog.TraceLevel),
		Prepare: func(req *http.Request) {
			q := req.URL.Query()
			q.Set("ApiKey", "token")
			req.URL.RawQuery = q.Encode()
		},
		SecretParams: []string{"ApiKey"},
	})
	if err := c.Send(http.MethodGet, "", nil); !errors.Is(err, autoscan.ErrTargetUnavailable) {
		t.Fatalf("Got error %v, want %v", err, autoscan.ErrTargetUnavailable)
	}

	out := buf.String()
	if !strings.Contains(out, "Request failed") || !strings.Contains(out, "user:xxxxx@") {
		t.Fatalf("expected a redacted failed-request log line, got %q", out)
	}
	if strings.Contains(out, "secret") || strings.Contains(out, "token") {
		t.Errorf("log output contains a credential: %s", out)
	}
}
