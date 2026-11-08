package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWaitForConfigOpensExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(path, []byte("port: 3030\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	file, err := waitForConfig(path, time.Hour)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	file.Close()
}

func TestWaitForConfigWaitsForMissingAndEmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yml")

	go func() {
		time.Sleep(50 * time.Millisecond)
		// an empty file is not ready yet
		_ = os.WriteFile(path, nil, 0o600)
		time.Sleep(50 * time.Millisecond)
		_ = os.WriteFile(path, []byte("port: 3030\n"), 0o600)
	}()

	done := make(chan error, 1)
	go func() {
		file, err := waitForConfig(path, 10*time.Millisecond)
		if err == nil {
			info, statErr := file.Stat()
			if statErr == nil && info.Size() == 0 {
				err = errors.New("opened the config file while it was empty")
			}
			file.Close()
		}
		done <- err
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("waitForConfig did not return after the config file appeared")
	}
}

func TestWaitForConfigReturnsOtherErrors(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(parent, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := waitForConfig(filepath.Join(parent, "config.yml"), time.Hour)
	if err == nil || errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("expected a non-ErrNotExist error, got %v", err)
	}
}
