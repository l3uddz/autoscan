package main

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestDefaultConfigDirectoryUsesUserConfigDirWhenAvailable(t *testing.T) {
	binDir := setBinaryPath(t)
	warnConfigDirOnce = sync.Once{}

	configRoot := filepath.Join(t.TempDir(), "user-config")
	stubUserConfigDir(t, func() (string, error) {
		return configRoot, nil
	})

	dir := defaultConfigDirectory("autoscan", "config.yml")
	want := filepath.Join(configRoot, "autoscan")
	if dir != want {
		t.Fatalf("expected %s, got %s", want, dir)
	}

	if _, err := os.Stat(want); err != nil {
		t.Fatalf("expected config directory %s to exist: %v", want, err)
	}

	if bcd := getBinaryPath(); bcd != binDir {
		t.Fatalf("expected binary dir %s, got %s", binDir, bcd)
	}
}

func TestDefaultConfigDirectoryFallsBackWhenUserConfigDirUnavailable(t *testing.T) {
	expected := setBinaryPath(t)
	warnConfigDirOnce = sync.Once{}

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
	warnConfigDirOnce = sync.Once{}

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
