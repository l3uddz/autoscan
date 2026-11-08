package jellyfin

import (
	"net/http"
	"net/http/httptest"
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
