package main

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestDefaultConfigDirectoryUsesUserConfigDirWhenAvailable(t *testing.T) {
	binDir := setBinaryPath(t)

	configRoot := filepath.Join(t.TempDir(), "user-config")
	stubUserConfigDir(t, func() (string, error) {
		return configRoot, nil
	})

	dir := defaultConfigDirectory("autoscan", "config.yml")
	want := filepath.Join(configRoot, "autoscan")
	if dir != want {
		t.Fatalf("expected %s, got %s", want, dir)
	}

	info, err := os.Stat(want)
	if err != nil {
		t.Fatalf("expected config directory %s to exist: %v", want, err)
	}
	// The directory holds the tokens, so only the owner can read it.
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Errorf("config directory mode = %o, want 700", perm)
	}

	if bcd := getBinaryPath(); bcd != binDir {
		t.Fatalf("expected binary dir %s, got %s", binDir, bcd)
	}
}

func TestDefaultConfigDirectoryFallsBackWhenUserConfigDirUnavailable(t *testing.T) {
	expected := setBinaryPath(t)

	stubUserConfigDir(t, func() (string, error) {
		return "", errors.New("missing home")
	})

	dir := defaultConfigDirectory("autoscan", "config.yml")
	if dir != expected {
		t.Fatalf("expected fallback to binary dir %s, got %s", expected, dir)
	}
}

func TestDefaultConfigDirectoryFallsBackWhenConfigDirCannotBeCreated(t *testing.T) {
	expected := setBinaryPath(t)

	tempFile := filepath.Join(t.TempDir(), "config-root")
	if err := os.WriteFile(tempFile, []byte("x"), 0o644); err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}

	stubUserConfigDir(t, func() (string, error) {
		return tempFile, nil
	})

	dir := defaultConfigDirectory("autoscan", "config.yml")
	if dir != expected {
		t.Fatalf("expected fallback to binary dir %s, got %s", expected, dir)
	}
}

func TestApplyDefaultPathsSkipsConfigDirWhenAllPathsSet(t *testing.T) {
	stubUserConfigDir(t, func() (string, error) {
		t.Fatal("resolved the user config directory although all paths are set")
		return "", nil
	})

	config, database, logFile := "/config/config.yml", "/config/autoscan.db", "/config/activity.log"
	applyDefaultPaths(&config, &database, &logFile)

	if config != "/config/config.yml" || database != "/config/autoscan.db" || logFile != "/config/activity.log" {
		t.Fatalf("expected paths to stay unchanged, got %s, %s, %s", config, database, logFile)
	}
}

func TestApplyDefaultPathsFillsOnlyEmptyPaths(t *testing.T) {
	setBinaryPath(t)

	configRoot := filepath.Join(t.TempDir(), "user-config")
	stubUserConfigDir(t, func() (string, error) {
		return configRoot, nil
	})

	config, database, logFile := "/config/config.yml", "", ""
	applyDefaultPaths(&config, &database, &logFile)

	dir := filepath.Join(configRoot, "autoscan")
	if config != "/config/config.yml" {
		t.Fatalf("expected config path to stay unchanged, got %s", config)
	}
	if want := filepath.Join(dir, "autoscan.db"); database != want {
		t.Fatalf("expected database path %s, got %s", want, database)
	}
	if want := filepath.Join(dir, "activity.log"); logFile != want {
		t.Fatalf("expected log path %s, got %s", want, logFile)
	}
}

func TestDecodeConfigKeepsDefaultsForUnsetKeys(t *testing.T) {
	c, err := decodeConfig(strings.NewReader("port: 4040\nscan-delay: 1s\n"))
	if err != nil {
		t.Fatalf("decodeConfig returned %v", err)
	}

	if c.Port != 4040 || time.Duration(c.ScanDelay) != time.Second {
		t.Errorf("port and scan delay = %d, %v, want 4040, 1s", c.Port, time.Duration(c.ScanDelay))
	}
	if time.Duration(c.MinimumAge) != 10*time.Minute || time.Duration(c.ScanStats) != time.Hour || !slices.Equal(c.Host, []string{""}) {
		t.Errorf("defaults = %v, %v, %q, want 10m, 1h, [\"\"]", time.Duration(c.MinimumAge), time.Duration(c.ScanStats), c.Host)
	}
}

func TestDecodeConfigAcceptsNumericDurations(t *testing.T) {
	c, err := decodeConfig(strings.NewReader("minimum-age: 0\nscan-delay: 5000000000\nscan-stats: 0\n"))
	if err != nil {
		t.Fatalf("decodeConfig returned %v for numeric durations", err)
	}
	if time.Duration(c.MinimumAge) != 0 || time.Duration(c.ScanDelay) != 5*time.Second || time.Duration(c.ScanStats) != 0 {
		t.Errorf("durations = (%v, %v, %v), want (0s, 5s, 0s)",
			time.Duration(c.MinimumAge), time.Duration(c.ScanDelay), time.Duration(c.ScanStats))
	}

	for _, invalid := range []string{"scan-stats: invalid\n", "scan-stats: true\n", "scan-stats: [0]\n"} {
		if _, err := decodeConfig(strings.NewReader(invalid)); err == nil {
			t.Errorf("decodeConfig returned nil error for %q", invalid)
		}
	}
}

func TestDecodeConfigZeroesExplicitNulls(t *testing.T) {
	const input = "minimum-age: null\nscan-delay: ~\nscan-stats:\nport: null\nhost: null\n"
	c, err := decodeConfig(strings.NewReader(input))
	if err != nil {
		t.Fatalf("decodeConfig returned %v", err)
	}
	if time.Duration(c.MinimumAge) != 0 || time.Duration(c.ScanDelay) != 0 || time.Duration(c.ScanStats) != 0 {
		t.Errorf("durations = (%v, %v, %v), want all 0",
			time.Duration(c.MinimumAge), time.Duration(c.ScanDelay), time.Duration(c.ScanStats))
	}
	if c.Port != 0 {
		t.Errorf("port = %d, want 0", c.Port)
	}
	if c.Host != nil {
		t.Errorf("host = %q, want nil", c.Host)
	}
}

func TestDecodeConfigRejectsUnknownKey(t *testing.T) {
	if _, err := decodeConfig(strings.NewReader("prot: 4040\n")); err == nil {
		t.Fatal("decodeConfig returned nil error for an unknown key")
	}
}

func TestDecodeConfigRejectsDuplicateKey(t *testing.T) {
	if _, err := decodeConfig(strings.NewReader("port: 3030\nport: 4040\n")); err == nil {
		t.Fatal("decodeConfig returned nil error for a duplicate key")
	}
}

func stubUserConfigDir(t *testing.T, fn func() (string, error)) {
	t.Helper()
	original := userConfigDirFunc
	userConfigDirFunc = fn
	t.Cleanup(func() {
		userConfigDirFunc = original
	})
}

func setBinaryPath(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	binDir := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("failed to create bin dir: %v", err)
	}

	original := os.Args[0]
	os.Args[0] = filepath.Join(binDir, "autoscan")
	t.Cleanup(func() {
		os.Args[0] = original
	})

	return binDir
}

func TestValidateConfig(t *testing.T) {
	type Test struct {
		Name         string
		YAML         string
		WantErr      string
		WantWarnings []string
	}

	var testCases = []Test{
		{Name: "Defaults", YAML: "{}"},
		{
			Name: "One trigger without a name",
			YAML: "triggers:\n  sonarr:\n    - priority: 1\n",
		},
		{
			Name:    "Empty host list",
			YAML:    "host: []\n",
			WantErr: "host must contain at least one address",
		},
		{
			Name:    "Port out of range",
			YAML:    "port: 70000\n",
			WantErr: "port",
		},
		{
			Name:    "Duplicate trigger names across types",
			YAML:    "triggers:\n  sonarr:\n    - name: arr\n  radarr:\n    - name: arr\n",
			WantErr: `trigger name "arr" is used more than once`,
		},
		{
			Name:    "Two triggers without a name",
			YAML:    "triggers:\n  sonarr:\n    - priority: 1\n    - priority: 2\n",
			WantErr: `trigger name "" is used more than once`,
		},
		{
			Name:    "Trigger named manual",
			YAML:    "triggers:\n  lidarr:\n    - name: manual\n",
			WantErr: `trigger name "manual" is reserved`,
		},
		{
			Name:    "Trigger name below the manual route",
			YAML:    "triggers:\n  sonarr:\n    - name: manual/\n",
			WantErr: `trigger name "manual/" is reserved`,
		},
		{
			Name:    "Trigger name nested under manual",
			YAML:    "triggers:\n  sonarr:\n    - name: manual/extra\n",
			WantErr: `trigger name "manual/extra" is reserved`,
		},
		{
			Name:    "Trigger name starting with slash",
			YAML:    "triggers:\n  sonarr:\n    - name: /sonarr\n",
			WantErr: `trigger name "/sonarr" must not start with /`,
		},
		{
			Name:    "Trigger name with a route pattern",
			YAML:    "triggers:\n  readarr:\n    - name: \"{id}\"\n",
			WantErr: `trigger name "{id}" contains`,
		},
		{
			Name: "Values that work but are likely mistakes",
			YAML: "minimum-age: -1m\nscan-delay: -1s\nscan-stats: -1h\n" +
				"authentication:\n  username: user\n" +
				"triggers:\n  manual:\n    verbosity: loud\n  inotify:\n    - priority: 1\n" +
				"targets:\n  autoscan:\n    - url: http://autoscan:3030\n      password: secret\n",
			WantWarnings: []string{
				"minimum-age is negative, so autoscan uses 0",
				"scan-delay is negative, so autoscan uses 0",
				"scan-stats is negative, so autoscan logs no scan stats",
				"authentication needs a username and a password, so the webhooks run without authentication",
				`manual trigger: verbosity "loud" is not a log level, so it uses the global log level`,
				"inotify-1 has no paths, so it watches nothing",
				"autoscan-1 needs a username and a password, so it sends requests without authentication",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			c, err := decodeConfig(strings.NewReader(tc.YAML))
			if err != nil {
				t.Fatal(err)
			}
			warnings, err := validateConfig(c)
			if tc.WantErr == "" && err != nil || tc.WantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.WantErr)) {
				t.Errorf("validateConfig error = %v, want %q", err, tc.WantErr)
			}
			if !slices.Equal(warnings, tc.WantWarnings) {
				t.Errorf("warnings = %q, want %q", warnings, tc.WantWarnings)
			}
		})
	}
}
