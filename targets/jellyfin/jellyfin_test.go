package jellyfin

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
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("[]"))
	}))
	defer srv.Close()

	var buf bytes.Buffer
	orig := log.Logger
	log.Logger = zerolog.New(&buf)
	defer func() { log.Logger = orig }()

	proxyURL := strings.Replace(srv.URL, "http://", "http://user:secret@", 1)
	if _, err := New(Config{URL: proxyURL, Token: "token", Verbosity: "debug"}); err != nil {
		t.Fatalf("New() returned error: %v", err)
	}

	if !strings.Contains(buf.String(), "Retrieved libraries") {
		t.Fatalf("expected a debug log line, got %q", buf.String())
	}
	if strings.Contains(buf.String(), "secret") {
		t.Errorf("log output contains the URL password: %s", buf.String())
	}
}
