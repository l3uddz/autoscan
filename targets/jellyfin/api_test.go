package jellyfin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rs/zerolog"
)

func TestRequestsUseAuthorizationHeader(t *testing.T) {
	const token = "0123456789abcdef"

	var gotAuth, gotLegacy string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotLegacy = r.Header.Get("X-Emby-Token")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	api := newAPIClient(srv.URL, token, zerolog.Nop())
	if err := api.Available(); err != nil {
		t.Fatalf("Available() returned error: %v", err)
	}

	if want := `MediaBrowser Token="` + token + `"`; gotAuth != want {
		t.Errorf("Authorization header = %q, want %q", gotAuth, want)
	}
	if gotLegacy != "" {
		t.Errorf("X-Emby-Token header = %q, want it unset", gotLegacy)
	}
}

func TestRequestsBehindBasicAuthProxyUseApiKeyQuery(t *testing.T) {
	const token = "0123456789abcdef"

	var gotUser, gotPass, gotAPIKey string
	var gotBasic bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser, gotPass, gotBasic = r.BasicAuth()
		gotAPIKey = r.URL.Query().Get("ApiKey")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	proxyURL := strings.Replace(srv.URL, "http://", "http://user:pass@", 1)
	api := newAPIClient(proxyURL, token, zerolog.Nop())
	if err := api.Available(); err != nil {
		t.Fatalf("Available() returned error: %v", err)
	}

	if !gotBasic || gotUser != "user" || gotPass != "pass" {
		t.Errorf("Basic auth = (%q, %q, %v), want (\"user\", \"pass\", true)", gotUser, gotPass, gotBasic)
	}
	if gotAPIKey != token {
		t.Errorf("ApiKey query = %q, want %q", gotAPIKey, token)
	}
}

func TestConnectionErrorDoesNotLeakToken(t *testing.T) {
	const token = "0123456789abcdef"

	srv := httptest.NewServer(http.NotFoundHandler())
	proxyURL := strings.Replace(srv.URL, "http://", "http://user:pass@", 1)
	srv.Close()

	api := newAPIClient(proxyURL, token, zerolog.Nop())
	err := api.Available()
	if err == nil {
		t.Fatal("Available() returned nil error for a closed server")
	}
	if strings.Contains(err.Error(), token) || strings.Contains(err.Error(), "pass@") {
		t.Errorf("error leaks credentials: %v", err)
	}
}
