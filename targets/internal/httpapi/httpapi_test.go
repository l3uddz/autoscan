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

func TestWithoutURLOmitsURL(t *testing.T) {
	requestErr := &url.Error{
		Op:  "Get",
		URL: "http://example.invalid/?token=example-token",
		Err: os.ErrDeadlineExceeded,
	}
	err := withoutURL(requestErr)
	if !errors.Is(err, os.ErrDeadlineExceeded) {
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
		Name        string
		Err         error
		Unavailable bool
	}

	var testCases = []Test{
		{
			Name:        "Timeout is unavailable",
			Err:         os.ErrDeadlineExceeded,
			Unavailable: true,
		},
		{
			Name: "Invalid JSON is not unavailable",
			Err:  json.Unmarshal([]byte("{"), new(struct{})),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			err := bodyError("decoding response", tc.Err)
			if !errors.Is(err, tc.Err) || errors.Is(err, autoscan.ErrTargetUnavailable) != tc.Unavailable {
				t.Errorf("Got error %v, want it to wrap %v with unavailable=%v", err, tc.Err, tc.Unavailable)
			}
		})
	}
}

func TestResponseErrorClassifies(t *testing.T) {
	type Test struct {
		Status      int
		Unavailable bool
	}

	var testCases = []Test{
		{http.StatusOK, false},
		{http.StatusNoContent, false},
		{http.StatusUnauthorized, false},
		{http.StatusForbidden, false},
		{http.StatusNotFound, true},
		{http.StatusInternalServerError, true},
		{http.StatusBadGateway, true},
		{http.StatusServiceUnavailable, true},
		{http.StatusGatewayTimeout, true},
		{http.StatusNotImplemented, false},
	}

	for _, tc := range testCases {
		t.Run(http.StatusText(tc.Status), func(t *testing.T) {
			body := &closeRecorder{Reader: strings.NewReader("")}
			res := &http.Response{StatusCode: tc.Status, Status: http.StatusText(tc.Status), Body: body}

			err := responseError(res, "invalid token")
			if tc.Status < 300 {
				if err != nil || body.closed {
					t.Errorf("Got error %v and closed=%v, want nil and an open body", err, body.closed)
				}
				return
			}

			if err == nil || errors.Is(err, autoscan.ErrTargetUnavailable) != tc.Unavailable {
				t.Errorf("Got error %v, want an error with unavailable=%v", err, tc.Unavailable)
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

// mustNew returns a Client for c and fails the test if New fails.
func mustNew(t *testing.T, c Config) *Client {
	t.Helper()
	client, err := New(c)
	if err != nil {
		t.Fatalf("New returned %v", err)
	}
	return client
}

// newTestClient returns a Client for url with a short timeout.
func newTestClient(t *testing.T, url string) *Client {
	c := mustNew(t, Config{BaseURL: url, Log: zerolog.Nop()})
	c.client.Timeout = 50 * time.Millisecond
	return c
}

func TestNewUsesTargetTimeout(t *testing.T) {
	if got := mustNew(t, Config{BaseURL: "http://example.invalid"}).client.Timeout; got != timeout {
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

	if err := newTestClient(t, srv.URL).Send(t.Context(), http.MethodGet, nil); !errors.Is(err, autoscan.ErrTargetUnavailable) {
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
	if err := newTestClient(t, srv.URL).GetJSON(t.Context(), &v); !errors.Is(err, autoscan.ErrTargetUnavailable) {
		t.Errorf("Got error %v, want %v", err, autoscan.ErrTargetUnavailable)
	}
}

func TestSendBuildsRequest(t *testing.T) {
	var got *http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r
	}))
	defer srv.Close()

	c := mustNew(t, Config{
		BaseURL: srv.URL + "/base/",
		Log:     zerolog.Nop(),
		Prepare: func(req *http.Request) { req.Header.Set("X-Token", "token") },
	})
	if err := c.Send(t.Context(), http.MethodPost, url.Values{"path": {"/mnt/tv"}}, "library", "sections", "1", "refresh"); err != nil {
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
	if h := got.Header.Get("Accept"); h != "application/json" {
		t.Errorf("Accept header = %q, want application/json", h)
	}
}

func TestFailedRequestLogRemovesSecrets(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()

	var buf strings.Builder
	c := mustNew(t, Config{
		BaseURL: strings.Replace(srv.URL, "http://", "http://user:secret@", 1),
		Log:     zerolog.New(&buf).Level(zerolog.TraceLevel),
		Prepare: func(req *http.Request) {
			q := req.URL.Query()
			q.Set("ApiKey", "token")
			req.URL.RawQuery = q.Encode()
		},
		SecretParams: []string{"ApiKey"},
	})
	if err := c.Send(t.Context(), http.MethodGet, nil); !errors.Is(err, autoscan.ErrTargetUnavailable) {
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

func TestRequestPathKeepsBaseURLPath(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.EscapedPath()
	}))
	defer srv.Close()

	type Test struct {
		Name string
		Base string
		Elem []string
		Want string
	}

	userinfo := strings.Replace(srv.URL, "http://", "http://user:secret@", 1)
	// Go dials the local system for an empty host, so a port-only URL works.
	// The proxy environment does not exempt an empty host, so the test
	// client uses a transport without a proxy.
	portOnly := "http://" + srv.URL[strings.LastIndex(srv.URL, ":"):]
	var testCases = []Test{
		{"Root", srv.URL, []string{"/"}, "/"},
		{"Root with slash", srv.URL + "/", []string{"/"}, "/"},
		{"Root with prefix", srv.URL + "/plex", []string{"/"}, "/plex/"},
		{"Root with prefix and slash", srv.URL + "/plex/", []string{"/"}, "/plex/"},
		{"Endpoint", srv.URL, []string{"library", "sections"}, "/library/sections"},
		{"Endpoint with slash", srv.URL + "/", []string{"library", "sections"}, "/library/sections"},
		{"Endpoint with prefix", srv.URL + "/plex", []string{"library", "sections", "1", "refresh"}, "/plex/library/sections/1/refresh"},
		{"Endpoint with prefix and slash", srv.URL + "/plex/", []string{"emby", "System", "Info"}, "/plex/emby/System/Info"},
		{"Endpoint with userinfo", userinfo, []string{"triggers", "manual"}, "/triggers/manual"},
		{"Endpoint with port only", portOnly, []string{"library", "sections"}, "/library/sections"},
	}

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			got = ""
			c := mustNew(t, Config{BaseURL: tc.Base, Log: zerolog.Nop()})
			c.client.Transport = &http.Transport{}
			if err := c.Send(t.Context(), http.MethodGet, nil, tc.Elem...); err != nil {
				t.Fatalf("Send returned %v", err)
			}
			if got != tc.Want {
				t.Errorf("request path = %q, want %q", got, tc.Want)
			}
		})
	}
}

func TestNewRejectsBaseURL(t *testing.T) {
	var testCases = map[string]string{
		"Query":        "http://user:secret@plex.invalid:32400?X-Plex-Token=token",
		"Empty query":  "http://user:secret@plex.invalid:32400/?",
		"Fragment":     "http://user:secret@plex.invalid:32400#secret",
		"No scheme":    "plex.invalid:32400",
		"Other scheme": "ftp://user:secret@plex.invalid",
		"No host":      "http://",
		"Opaque":       "http:plex.invalid:32400",
		"Invalid":      "http://user:secret@plex.invalid:port",
		"Empty":        "",
	}

	for name, raw := range testCases {
		t.Run(name, func(t *testing.T) {
			_, err := New(Config{BaseURL: raw})
			if err == nil {
				t.Fatal("New returned nil error")
			}
			if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "token") {
				t.Errorf("error %q contains a credential", err)
			}
		})
	}
}

func TestRequestDoesNotChangeBaseURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()

	c := mustNew(t, Config{
		BaseURL: srv.URL + "/plex",
		Log:     zerolog.Nop(),
		Prepare: func(req *http.Request) {
			q := req.URL.Query()
			q.Set("ApiKey", "token")
			req.URL.RawQuery = q.Encode()
		},
	})
	if err := c.Send(t.Context(), http.MethodGet, url.Values{"path": {"/mnt"}}, "library"); err != nil {
		t.Fatalf("Send returned %v", err)
	}

	if got, want := c.base.String(), srv.URL+"/plex"; got != want {
		t.Errorf("base URL = %q after a request, want %q", got, want)
	}
}

func TestRedirectToOtherMethodIsUnavailable(t *testing.T) {
	var methods []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		if r.URL.Path == "/old" {
			http.Redirect(w, r, "/new", http.StatusMovedPermanently)
		}
	}))
	defer srv.Close()

	err := mustNew(t, Config{BaseURL: srv.URL, Log: zerolog.Nop()}).Send(t.Context(), http.MethodPost, nil, "old")
	if !errors.Is(err, autoscan.ErrTargetUnavailable) {
		t.Errorf("Got error %v, want %v", err, autoscan.ErrTargetUnavailable)
	}
	if len(methods) != 1 {
		t.Errorf("server got %v, want only the POST", methods)
	}
}

func TestCheckRedirect(t *testing.T) {
	var testCases = []struct {
		Name    string
		From    string
		To      string
		Method  string
		WantErr bool
	}{
		{"Same host and port", "http://plex.invalid:32400/old", "http://plex.invalid:32400/new", http.MethodGet, false},
		{"Upgrade to https", "http://plex.invalid/old", "https://plex.invalid/new", http.MethodGet, false},
		{"Upgrade to https with default ports", "http://plex.invalid:80/old", "https://plex.invalid:443/new", http.MethodGet, false},
		{"Host in other case", "http://Plex.invalid:32400/old", "http://plex.invalid:32400/new", http.MethodGet, false},
		{"Default port left out", "http://plex.invalid:80/old", "http://plex.invalid/new", http.MethodGet, false},
		{"Other host", "http://plex.invalid/", "http://other.invalid/", http.MethodGet, true},
		{"Other port", "http://plex.invalid:32400/", "http://plex.invalid:8080/", http.MethodGet, true},
		{"Downgrade to http", "https://plex.invalid/", "http://plex.invalid/", http.MethodGet, true},
		{"Other method", "http://plex.invalid/old", "http://plex.invalid/new", http.MethodPost, true},
	}

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			first := httptest.NewRequest(http.MethodGet, tc.From, nil)
			next := httptest.NewRequest(tc.Method, tc.To, nil)
			if err := checkRedirect(next, []*http.Request{first}); (err != nil) != tc.WantErr {
				t.Errorf("checkRedirect = %v, want error %v", err, tc.WantErr)
			}
		})
	}
}

func TestCheckRedirectRefusesLaterDowngrade(t *testing.T) {
	via := []*http.Request{
		httptest.NewRequest(http.MethodGet, "http://plex.invalid/old", nil),
		httptest.NewRequest(http.MethodGet, "https://plex.invalid/new", nil),
	}
	next := httptest.NewRequest(http.MethodGet, "http://plex.invalid/final", nil)
	if err := checkRedirect(next, via); err == nil {
		t.Error("checkRedirect followed a redirect from https back to http")
	}
}

func TestRedirectToOtherPortIsUnavailable(t *testing.T) {
	var leaked string
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked = r.Header.Get("X-Token")
	}))
	defer other.Close()
	// Both servers listen on 127.0.0.1, on different ports.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL, http.StatusFound)
	}))
	defer srv.Close()

	c := mustNew(t, Config{
		BaseURL: srv.URL,
		Log:     zerolog.Nop(),
		Prepare: func(req *http.Request) { req.Header.Set("X-Token", "token") },
	})
	if err := c.Send(t.Context(), http.MethodGet, nil); !errors.Is(err, autoscan.ErrTargetUnavailable) {
		t.Errorf("Got error %v, want %v", err, autoscan.ErrTargetUnavailable)
	}
	if leaked != "" {
		t.Error("the token reached the other host")
	}
}

func TestRedirectOnSameHostIsFollowed(t *testing.T) {
	var gotToken string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/old" {
			http.Redirect(w, r, "/new", http.StatusMovedPermanently)
			return
		}
		gotToken = r.Header.Get("X-Token")
	}))
	defer srv.Close()

	c := mustNew(t, Config{
		BaseURL: srv.URL,
		Log:     zerolog.Nop(),
		Prepare: func(req *http.Request) { req.Header.Set("X-Token", "token") },
	})
	if err := c.Send(t.Context(), http.MethodGet, nil, "old"); err != nil {
		t.Fatalf("Send returned %v", err)
	}
	if gotToken != "token" {
		t.Errorf("X-Token after the redirect = %q, want token", gotToken)
	}
}
