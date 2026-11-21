package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/rs/zerolog/log"
	"gopkg.in/yaml.v2"
)

var userConfigDirFunc = os.UserConfigDir

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
		// The directory holds the tokens in config.yml, so only the owner
		// can read it. MkdirAll keeps the mode of an existing directory.
		mkErr := os.MkdirAll(acd, 0o700)
		if mkErr == nil {
			return acd
		}

		fmt.Fprintf(os.Stderr, "autoscan: failed ensuring config directory %s: %v\n", acd, mkErr)
	} else if err != nil {
		fmt.Fprintf(os.Stderr, "autoscan: unable to determine user config directory: %v\n", err)
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

// decodeConfig decodes the YAML configuration in r over the default values.
// It returns an error for a key that config does not define.
func decodeConfig(r io.Reader) (config, error) {
	c := config{
		MinimumAge: 10 * time.Minute,
		ScanDelay:  5 * time.Second,
		ScanStats:  1 * time.Hour,
		Host:       []string{""},
		Port:       3030,
	}

	decoder := yaml.NewDecoder(r)
	decoder.SetStrict(true)
	if err := decoder.Decode(&c); err != nil {
		return config{}, err
	}

	return c, nil
}
