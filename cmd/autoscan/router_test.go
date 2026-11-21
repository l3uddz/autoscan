package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/l3uddz/autoscan"
	"github.com/l3uddz/autoscan/migrate"
	"github.com/l3uddz/autoscan/processor"
	"github.com/l3uddz/autoscan/targets/plex"
	"github.com/l3uddz/autoscan/triggers/inotify"
	"github.com/l3uddz/autoscan/triggers/lidarr"
	"github.com/l3uddz/autoscan/triggers/radarr"
	"github.com/l3uddz/autoscan/triggers/readarr"
	"github.com/l3uddz/autoscan/triggers/sonarr"
)

func newTestRouter(t *testing.T) http.Handler {
	t.Helper()
	proc, _ := newRouterTestProcessor(t, processor.Config{})

	var c config
	c.Auth.Username = "user"
	c.Auth.Password = "pass"
	c.Triggers.Lidarr = []lidarr.Config{{Name: "lidarr"}}
	c.Triggers.Radarr = []radarr.Config{{Name: "radarr"}, {Name: "radarr4k"}}
	c.Triggers.Readarr = []readarr.Config{{Name: "readarr"}}
	c.Triggers.Sonarr = []sonarr.Config{{Name: "sonarr"}}

	return mustRouter(t, c, proc)
}

func mustRouter(t *testing.T, c config, proc *processor.Processor) http.Handler {
	t.Helper()
	router, err := getRouter(c, proc, nil)
	if err != nil {
		t.Fatal(err)
	}
	return router
}

func TestRouterReturnsTriggerError(t *testing.T) {
	proc, _ := newRouterTestProcessor(t, processor.Config{})

	var c config
	c.Triggers.Sonarr = []sonarr.Config{
		{Name: "sonarr", Rewrite: []autoscan.Rewrite{{From: "("}}},
		{Name: "sonarr4k", Rewrite: []autoscan.Rewrite{{From: "["}}},
	}

	_, err := getRouter(c, proc, nil)
	if err == nil || !strings.Contains(err.Error(), "trigger sonarr") || !strings.Contains(err.Error(), "trigger sonarr4k") {
		t.Fatalf("getRouter error = %v, want errors naming both sonarr and sonarr4k", err)
	}
}

func newRouterTestProcessor(t *testing.T, cfg processor.Config) (*processor.Processor, *sql.DB) {
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

	cfg.DB, cfg.Migrator = db, mg
	proc, err := processor.New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	return proc, db
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
		{"Processor status needs authentication", http.MethodGet, "/status", false, http.StatusUnauthorized},
		{"Processor status accepts authentication", http.MethodGet, "/status", true, http.StatusServiceUnavailable},
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

func TestStatusReportsDatabaseFailureWhileHealthResponds(t *testing.T) {
	proc, db := newRouterTestProcessor(t, processor.Config{})
	router := mustRouter(t, config{}, proc)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	status := requestProcessorStatus(t, router, http.StatusServiceUnavailable)
	if status.CanDeliver || !slices.Contains(status.Blockers, "database_unavailable") || status.OldestAge != nil || len(status.Targets) != 0 {
		t.Errorf("GET /status returned %+v, want a database blocker", status)
	}

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("GET /health returned %d, want 200", rec.Code)
	}
}

func TestStatusReportsPendingRetryAndSharedAnchor(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		anchor := filepath.Join(t.TempDir(), "mount.anchor")
		if err := os.WriteFile(anchor, nil, 0600); err != nil {
			t.Fatal(err)
		}
		const retryURL = "http://private-user:private-password@retry.example/private-path?token=private-token"
		failUntil := time.Now().Add(15 * time.Second)
		targets, err := newTargets("plex", []statusTestConfig{"http://healthy.example", retryURL}, func(_ context.Context, address statusTestConfig) (autoscan.Target, error) {
			if address == retryURL && time.Now().Before(failUntil) {
				return nil, fmt.Errorf("GET %s: %w", address, autoscan.ErrTargetUnavailable)
			}
			return statusTestTarget{}, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		proc, _ := newRouterTestProcessor(t, processor.Config{Anchors: []string{anchor}, Targets: targets})
		if err := proc.Add(
			autoscan.Scan{Folder: "/private/oldest", Time: time.Now().Add(-time.Minute)},
			autoscan.Scan{Folder: "/private/newer", Time: time.Now().Add(-time.Second).In(time.FixedZone("EST", -5*60*60))},
		); err != nil {
			t.Fatal(err)
		}

		ctx, stop := context.WithCancel(t.Context())
		defer stop()
		go proc.Run(ctx)
		synctest.Wait()
		router := mustRouter(t, config{}, proc)

		status := requestProcessorStatus(t, router, http.StatusOK)
		if !status.CanDeliver || len(status.Blockers) != 0 || status.OldestAge == nil || *status.OldestAge != 60 {
			t.Fatalf("unexpected status while one target retries: %+v", status)
		}
		if len(status.Targets) != 2 || status.Targets[0].ID != "plex-1" || status.Targets[0].Pending != 0 {
			t.Fatalf("unexpected targets: %+v", status.Targets)
		}
		retry := status.Targets[1]
		if retry.ID != "plex-2" || retry.Pending != 2 || retry.LastError != "target unavailable" || retry.NextRetry == nil || !retry.NextRetry.Equal(failUntil) {
			t.Fatalf("unexpected retry state: %+v", retry)
		}

		if err := os.Remove(anchor); err != nil {
			t.Fatal(err)
		}
		blocked := requestProcessorStatus(t, router, http.StatusServiceUnavailable)
		if blocked.CanDeliver || !slices.Equal(blocked.Blockers, []string{"anchor_unavailable"}) || len(blocked.Targets) != 2 || blocked.Targets[1].Pending != 2 {
			t.Fatalf("unexpected status with missing anchor: %+v", blocked)
		}

		if err := os.WriteFile(anchor, nil, 0600); err != nil {
			t.Fatal(err)
		}
		time.Sleep(15 * time.Second)
		synctest.Wait()
		healthy := requestProcessorStatus(t, router, http.StatusOK)
		if !healthy.CanDeliver || healthy.OldestAge != nil || len(healthy.Targets) != 2 || healthy.Targets[1].Pending != 0 || healthy.Targets[1].LastError != "" || healthy.Targets[1].NextRetry != nil {
			t.Fatalf("unexpected status after recovery: %+v", healthy)
		}
	})
}

func TestStatusReportsInotifyPathsWithoutNames(t *testing.T) {
	proc, _ := newRouterTestProcessor(t, processor.Config{})
	var cfg inotify.Config
	if err := yaml.Unmarshal([]byte("paths:\n  - path: /private/tv\n  - path: /private/movies\n"), &cfg); err != nil {
		t.Fatal(err)
	}
	watcher, err := inotify.New(cfg, proc.Add)
	if err != nil {
		t.Fatal(err)
	}
	router, err := getRouter(config{}, proc, []*inotify.Trigger{watcher})
	if err != nil {
		t.Fatal(err)
	}

	// No target means no delivery. An inotify path does not change that.
	status := requestProcessorStatus(t, router, http.StatusServiceUnavailable)
	if len(status.Inotify) != 1 || status.Inotify[0].ID != "inotify-1" || status.Inotify[0].Paths != 2 || status.Inotify[0].Watching != 0 {
		t.Errorf("GET /status inotify = %+v, want inotify-1 with 2 paths and none watched", status.Inotify)
	}
}

// statusTestConfig is a target configuration that is its URL.
type statusTestConfig string

func (c statusTestConfig) BaseURL() string { return string(c) }
func (statusTestConfig) Validate() error   { return nil }

type statusTestTarget struct{}

func (statusTestTarget) Available(context.Context) error           { return nil }
func (statusTestTarget) Scan(context.Context, autoscan.Scan) error { return nil }

type statusResponse struct {
	CanDeliver bool     `json:"can_deliver"`
	Blockers   []string `json:"blockers"`
	OldestAge  *int64   `json:"oldest_pending_scan_age_seconds"`
	Targets    []struct {
		ID        string     `json:"id"`
		Pending   int        `json:"pending"`
		LastError string     `json:"last_error"`
		NextRetry *time.Time `json:"next_retry"`
	} `json:"targets"`
	Inotify []struct {
		ID       string `json:"id"`
		Paths    int    `json:"paths"`
		Watching int    `json:"watching"`
	} `json:"inotify"`
}

func requestProcessorStatus(t *testing.T, router http.Handler, wantCode int) statusResponse {
	t.Helper()
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/status", nil))
	if rec.Code != wantCode || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("GET /status returned %d with headers %v, want %d and JSON", rec.Code, rec.Header(), wantCode)
	}
	for _, secret := range []string{"private-user", "private-password", "private-path", "private-token", "/private/", "mount.anchor"} {
		if strings.Contains(rec.Body.String(), secret) {
			t.Fatalf("GET /status exposed %q", secret)
		}
	}
	var status statusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	return status
}

func TestNewTargetsRejectsInvalidConfiguration(t *testing.T) {
	_, err := newTargets("plex", []plex.Config{{URL: "http://plex:32400"}, {URL: "plex:32400"}}, plex.New)
	if err == nil || !strings.Contains(err.Error(), "plex-2") {
		t.Errorf("newTargets returned %v, want an error for plex-2", err)
	}
}
