package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	"go.yaml.in/yaml/v3"

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

// loadConfig waits for the config file at path, then decodes and checks it.
// It logs each warning of the check.
func loadConfig(path string) (config, error) {
	file, err := waitForConfig(path, 5*time.Second)
	if err != nil {
		return config{}, fmt.Errorf("open config: %w", err)
	}
	defer file.Close()

	c, err := decodeConfig(file)
	if err != nil {
		return config{}, fmt.Errorf("decode config: %w", err)
	}
	warnings, err := validateConfig(c)
	for _, warning := range warnings {
		log.Warn().Msg(warning)
	}
	if err != nil {
		return config{}, fmt.Errorf("check config: %w", err)
	}

	return c, nil
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

// yamlDuration decodes a YAML duration from either a Go duration string such
// as "5s" or a bare numeric nanosecond value such as 0, keeping compatibility
// with yaml.v2 configurations.
type yamlDuration time.Duration

func (d *yamlDuration) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind != yaml.ScalarNode {
		return fmt.Errorf("line %d: cannot unmarshal %s into time.Duration", value.Line, value.ShortTag())
	}
	if value.ShortTag() == "!!str" {
		parsed, err := time.ParseDuration(value.Value)
		if err != nil {
			return fmt.Errorf("line %d: cannot unmarshal %q into time.Duration: %w", value.Line, value.Value, err)
		}
		*d = yamlDuration(parsed)
		return nil
	}
	var ns int64
	if err := value.Decode(&ns); err != nil {
		return err
	}
	*d = yamlDuration(ns)
	return nil
}

// decodeConfig decodes the YAML configuration in r over the default values.
// It returns an error for a key that config does not define. Explicit YAML
// nulls zero the corresponding defaulted fields, matching yaml.v2 behavior.
func decodeConfig(r io.Reader) (config, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return config{}, err
	}

	c := config{
		MinimumAge: yamlDuration(10 * time.Minute),
		ScanDelay:  yamlDuration(5 * time.Second),
		ScanStats:  yamlDuration(1 * time.Hour),
		Host:       []string{""},
		Port:       3030,
	}

	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	decoder.KnownFields(true)
	if err := decoder.Decode(&c); err != nil {
		return config{}, err
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return config{}, err
	}
	applyExplicitNulls(&doc, &c)

	return c, nil
}

// applyExplicitNulls zeroes defaulted top-level fields that the YAML document
// explicitly sets to null (such as "scan-stats:" or "minimum-age: null").
// yaml.v3 leaves pre-initialized struct fields untouched on null, whereas
// yaml.v2 zeroed them.
func applyExplicitNulls(doc *yaml.Node, c *config) {
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return
	}
	mapping := doc.Content[0].Content
	for i := 0; i+1 < len(mapping); i += 2 {
		if !isNullNode(mapping[i+1]) {
			continue
		}
		switch mapping[i].Value {
		case "minimum-age":
			c.MinimumAge = 0
		case "scan-delay":
			c.ScanDelay = 0
		case "scan-stats":
			c.ScanStats = 0
		case "port":
			c.Port = 0
		case "host":
			c.Host = nil
		}
	}
}

func isNullNode(n *yaml.Node) bool {
	if n.Kind == yaml.AliasNode && n.Alias != nil {
		n = n.Alias
	}
	return n.Kind == yaml.ScalarNode && n.ShortTag() == "!!null"
}

// validateConfig returns an error for each value of c that cannot work, and
// a warning for each value that works but is likely a mistake. The trigger
// and target packages check their other values when autoscan creates them.
func validateConfig(c config) (warnings []string, err error) {
	var errs []error
	if len(c.Host) == 0 {
		errs = append(errs, errors.New("host must contain at least one address"))
	}
	if c.Port < 0 || c.Port > 65535 {
		errs = append(errs, fmt.Errorf("port %d is not between 0 and 65535", c.Port))
	}
	errs = append(errs, checkTriggerNames(c)...)

	for _, d := range []struct {
		name  string
		value time.Duration
		then  string
	}{
		{"minimum-age", time.Duration(c.MinimumAge), "autoscan uses 0"},
		{"scan-delay", time.Duration(c.ScanDelay), "autoscan uses 0"},
		{"scan-stats", time.Duration(c.ScanStats), "autoscan logs no scan stats"},
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
			case strings.HasPrefix(t.Name, "/"):
				errs = append(errs, fmt.Errorf("trigger name %q must not start with /", t.Name))
			case t.Name == "manual" || strings.HasPrefix(t.Name, "manual/"):
				errs = append(errs, fmt.Errorf("trigger name %q is reserved for the manual trigger", t.Name))
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
