package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
)

var (
	userConfigDirFunc = os.UserConfigDir
	warnConfigDirOnce sync.Once
)

// applyDefaultPaths sets each empty path to a file in the default config
// directory. It resolves that directory only if a path is empty, because
// defaultConfigDirectory can create a directory. A container that sets all
// paths then never touches $HOME.
func applyDefaultPaths(config, database, logFile *string) {
	if *config != "" && *database != "" && *logFile != "" {
		return
	}

	dir := defaultConfigDirectory("autoscan", "config.yml")
	if *config == "" {
		*config = filepath.Join(dir, "config.yml")
	}
	if *database == "" {
		*database = filepath.Join(dir, "autoscan.db")
	}
	if *logFile == "" {
		*logFile = filepath.Join(dir, "activity.log")
	}
}

func defaultConfigDirectory(app string, filename string) string {
	// binary path
	bcd := getBinaryPath()
	if _, err := os.Stat(filepath.Join(bcd, filename)); err == nil {
		// there is a config file in the binary path
		// so use this directory as the default
		return bcd
	}

	// config dir
	ucd, err := userConfigDirFunc()
	if err == nil && ucd != "" {
		acd := filepath.Join(ucd, app)
		mkErr := os.MkdirAll(acd, os.ModePerm)
		if mkErr == nil {
			return acd
		}

		warnConfigDirOnce.Do(func() {
			fmt.Fprintf(os.Stderr, "autoscan: failed ensuring config directory %s: %v\n", acd, mkErr)
		})
	} else if err != nil {
		warnConfigDirOnce.Do(func() {
			fmt.Fprintf(os.Stderr, "autoscan: unable to determine user config directory: %v\n", err)
		})
	}

	return bcd
}

func getBinaryPath() string {
	// get current binary path
	dir, err := filepath.Abs(filepath.Dir(os.Args[0]))
	if err != nil {
		// get current working dir
		if dir, err = os.Getwd(); err != nil {
			panic(fmt.Sprintf("getwd: %v", err))
		}
	}

	return dir
}

// waitForConfig opens the config file at path. If the file is missing or
// empty, it checks again after each interval until the file has content.
// A container can then start before the user creates config.yml.
func waitForConfig(path string, interval time.Duration) (*os.File, error) {
	waited := false
	for {
		info, err := os.Stat(path)
		switch {
		case err == nil && info.Size() > 0:
			if waited {
				log.Info().Str("path", path).Msg("Found config")
			}
			return os.Open(path)
		case err != nil && !errors.Is(err, fs.ErrNotExist):
			return nil, err
		}

		if !waited {
			log.Warn().
				Str("path", path).
				Dur("interval", interval).
				Msg("Config is missing or empty, waiting for it")
			waited = true
		}
		time.Sleep(interval)
	}
}
