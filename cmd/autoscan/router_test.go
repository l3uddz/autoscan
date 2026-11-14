package main

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/l3uddz/autoscan/migrate"
	"github.com/l3uddz/autoscan/processor"
	"github.com/l3uddz/autoscan/triggers/lidarr"
	"github.com/l3uddz/autoscan/triggers/radarr"
	"github.com/l3uddz/autoscan/triggers/readarr"
	"github.com/l3uddz/autoscan/triggers/sonarr"
)

func newTestRouter(t *testing.T) http.Handler {
	t.Helper()

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)

	mg, err := migrate.New(db, "migrations")
	if err != nil {
		t.Fatal(err)
	}

	proc, err := processor.New(processor.Config{Db: db, Mg: mg})
	if err != nil {
		t.Fatal(err)
	}

	var c config
	c.Auth.Username = "user"
	c.Auth.Password = "pass"
	c.Triggers.Lidarr = []lidarr.Config{{Name: "lidarr"}}
	c.Triggers.Radarr = []radarr.Config{{Name: "radarr"}, {Name: "radarr4k"}}
	c.Triggers.Readarr = []readarr.Config{{Name: "readarr"}}
	c.Triggers.Sonarr = []sonarr.Config{{Name: "sonarr"}}

	return getRouter(c, proc)
}

func TestRouter(t *testing.T) {
	router := newTestRouter(t)

	type Test struct {
		Name   string
		Method string
		Path   string
		Auth   bool
		Want   int
	}

	var testCases = []Test{
		{"Health needs no authentication", http.MethodGet, "/health", false, http.StatusOK},
		{"Lidarr test event", http.MethodPost, "/triggers/lidarr", true, http.StatusOK},
		{"Radarr test event", http.MethodPost, "/triggers/radarr", true, http.StatusOK},
		{"Second Radarr trigger", http.MethodPost, "/triggers/radarr4k", true, http.StatusOK},
		{"Readarr test event", http.MethodPost, "/triggers/readarr", true, http.StatusOK},
		{"Sonarr test event", http.MethodPost, "/triggers/sonarr", true, http.StatusOK},
		{"Manual trigger page", http.MethodGet, "/triggers/manual", true, http.StatusOK},
		{"Triggers need authentication", http.MethodPost, "/triggers/sonarr", false, http.StatusUnauthorized},
		{"Unknown trigger", http.MethodPost, "/triggers/unknown", true, http.StatusNotFound},
		{"Webhook triggers accept only POST", http.MethodGet, "/triggers/sonarr", true, http.StatusMethodNotAllowed},
	}

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			req := httptest.NewRequest(tc.Method, tc.Path, strings.NewReader(`{"eventType":"Test"}`))
			if tc.Auth {
				req.SetBasicAuth("user", "pass")
			}

			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			if rec.Code != tc.Want {
				t.Errorf("%s %s returned %d, want %d", tc.Method, tc.Path, rec.Code, tc.Want)
			}
		})
	}
}
