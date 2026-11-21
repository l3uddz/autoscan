package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	"gopkg.in/yaml.v2"

	"github.com/l3uddz/autoscan"
	"github.com/l3uddz/autoscan/targets/plex"
	"github.com/l3uddz/autoscan/triggers/webhook"
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

// validateConfig returns an error for each value of c that cannot work, and
// a warning for each value that works but is likely a mistake. The trigger
// and target packages check their other values when autoscan creates them.
func validateConfig(c config) (warnings []string, err error) {
	var errs []error
	if c.Port < 0 || c.Port > 65535 {
		errs = append(errs, fmt.Errorf("port %d is not between 0 and 65535", c.Port))
	}
	errs = append(errs, checkTriggerNames(c)...)

	for _, d := range []struct {
		name  string
		value time.Duration
		then  string
	}{
		{"minimum-age", c.MinimumAge, "autoscan uses 0"},
		{"scan-delay", c.ScanDelay, "autoscan uses 0"},
		{"scan-stats", c.ScanStats, "autoscan logs no scan stats"},
	} {
		if d.value < 0 {
			warnings = append(warnings, fmt.Sprintf("%s is negative, so %s", d.name, d.then))
		}
	}
	if (c.Auth.Username == "") != (c.Auth.Password == "") {
		warnings = append(warnings, "authentication needs a username and a password, so the webhooks run without authentication")
	}
	warnings = append(warnings, verbosityWarnings(c)...)
	for i, t := range c.Triggers.Inotify {
		if len(t.Paths) == 0 {
			warnings = append(warnings, fmt.Sprintf("inotify-%d has no paths, so it watches nothing", i+1))
		}
	}
	for i, t := range c.Targets.Autoscan {
		if (t.User == "") != (t.Pass == "") {
			warnings = append(warnings, fmt.Sprintf("autoscan-%d needs a username and a password, so it sends requests without authentication", i+1))
		}
	}

	return warnings, errors.Join(errs...)
}

// checkTriggerNames returns an error for each webhook trigger name that
// breaks the routes below /triggers. Every -arr trigger shares those
// routes, so a name must be unique across all types. One trigger without a
// name is allowed: it gets the route /triggers/.
func checkTriggerNames(c config) []error {
	var errs []error
	seen := make(map[string]bool)
	for _, triggers := range [][]webhook.Config{c.Triggers.Lidarr, c.Triggers.Radarr, c.Triggers.Readarr, c.Triggers.Sonarr} {
		for _, t := range triggers {
			switch {
			case seen[t.Name]:
				errs = append(errs, fmt.Errorf("trigger name %q is used more than once", t.Name))
			case t.Name == "manual":
				errs = append(errs, errors.New(`trigger name "manual" is reserved for the manual trigger`))
			case strings.ContainsAny(t.Name, "{}*"):
				errs = append(errs, fmt.Errorf("trigger name %q contains a route pattern character: {, }, or *", t.Name))
			}
			seen[t.Name] = true
		}
	}
	return errs
}

// verbosityWarnings returns a warning for each verbosity that is not a log
// level. The trigger or target then uses the global log level.
func verbosityWarnings(c config) []string {
	type option struct{ owner, verbosity string }
	options := []option{{"manual trigger", c.Triggers.Manual.Verbosity}}
	for i, t := range c.Triggers.Inotify {
		options = append(options, option{fmt.Sprintf("inotify-%d", i+1), t.Verbosity})
	}
	for _, triggers := range []struct {
		kind    string
		configs []webhook.Config
	}{{"lidarr", c.Triggers.Lidarr}, {"radarr", c.Triggers.Radarr}, {"readarr", c.Triggers.Readarr}, {"sonarr", c.Triggers.Sonarr}} {
		for i, t := range triggers.configs {
			options = append(options, option{fmt.Sprintf("%s-%d", triggers.kind, i+1), t.Verbosity})
		}
	}
	for i, t := range c.Targets.Autoscan {
		options = append(options, option{fmt.Sprintf("autoscan-%d", i+1), t.Verbosity})
	}
	// The Plex, Emby, and Jellyfin configurations share one type.
	for _, targets := range []struct {
		kind    string
		configs []plex.Config
	}{{"plex", c.Targets.Plex}, {"emby", c.Targets.Emby}, {"jellyfin", c.Targets.Jellyfin}} {
		for i, t := range targets.configs {
			options = append(options, option{fmt.Sprintf("%s-%d", targets.kind, i+1), t.Verbosity})
		}
	}

	var warnings []string
	for _, o := range options {
		if _, ok := autoscan.ParseVerbosity(o.verbosity); !ok && o.verbosity != "" {
			warnings = append(warnings, fmt.Sprintf("%s: verbosity %q is not a log level, so it uses the global log level", o.owner, o.verbosity))
		}
	}
	return warnings
}
