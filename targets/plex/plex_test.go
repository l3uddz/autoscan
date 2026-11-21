package plex

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func TestNewDoesNotLogURLPassword(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()

	var buf bytes.Buffer
	orig := log.Logger
	log.Logger = zerolog.New(&buf)
	defer func() { log.Logger = orig }()

	proxyURL := strings.Replace(srv.URL, "http://", "http://user:secret@", 1)
	if _, err := New(t.Context(), Config{URL: proxyURL, Token: "token", Verbosity: "trace"}); err == nil {
		t.Fatal("New() returned nil error for a 404 response")
	}

	out := buf.String()
	if !strings.Contains(out, "Request failed") || !strings.Contains(out, "user:xxxxx@") {
		t.Fatalf("expected a redacted failed-request log line, got %q", out)
	}
	if strings.Contains(out, "secret") {
		t.Errorf("log output contains the URL password: %s", out)
	}
}

func TestIsSupportedVersion(t *testing.T) {
	for version, want := range map[string]bool{
		"1.20.0.3125": true,
		"1.32.5":      true,
		"2.0":         true,
		"1.19.9":      false,
		"0.9":         false,
		"1":           false,
		"":            false,
		"2.x":         false,
		"x.30":        false,
	} {
		if got := isSupportedVersion(version); got != want {
			t.Errorf("isSupportedVersion(%q) = %v, want %v", version, got, want)
		}
	}
}
