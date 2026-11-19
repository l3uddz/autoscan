package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/alecthomas/kong"
	"github.com/natefinch/lumberjack"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"gopkg.in/yaml.v2"

	"github.com/l3uddz/autoscan"
	"github.com/l3uddz/autoscan/migrate"
	"github.com/l3uddz/autoscan/processor"
	ast "github.com/l3uddz/autoscan/targets/autoscan"
	"github.com/l3uddz/autoscan/targets/emby"
	"github.com/l3uddz/autoscan/targets/jellyfin"
	"github.com/l3uddz/autoscan/targets/plex"
	"github.com/l3uddz/autoscan/triggers/inotify"
	"github.com/l3uddz/autoscan/triggers/lidarr"
	"github.com/l3uddz/autoscan/triggers/manual"
	"github.com/l3uddz/autoscan/triggers/radarr"
	"github.com/l3uddz/autoscan/triggers/readarr"
	"github.com/l3uddz/autoscan/triggers/sonarr"

	// sqlite3 driver
	_ "modernc.org/sqlite"
)

type config struct {
	// General configuration
	Host       []string      `yaml:"host"`
	Port       int           `yaml:"port"`
	MinimumAge time.Duration `yaml:"minimum-age"`
	ScanDelay  time.Duration `yaml:"scan-delay"`
	ScanStats  time.Duration `yaml:"scan-stats"`
	Anchors    []string      `yaml:"anchors"`

	// Authentication for the webhook triggers
	Auth struct {
		Username string `yaml:"username"`
		Password string `yaml:"password"`
	} `yaml:"authentication"`

	// Triggers
	Triggers struct {
		Manual  manual.Config    `yaml:"manual"`
		Inotify []inotify.Config `yaml:"inotify"`
		Lidarr  []lidarr.Config  `yaml:"lidarr"`
		Radarr  []radarr.Config  `yaml:"radarr"`
		Readarr []readarr.Config `yaml:"readarr"`
		Sonarr  []sonarr.Config  `yaml:"sonarr"`
	} `yaml:"triggers"`

	// autoscan.Target
	Targets struct {
		Autoscan []ast.Config      `yaml:"autoscan"`
		Emby     []emby.Config     `yaml:"emby"`
		Jellyfin []jellyfin.Config `yaml:"jellyfin"`
		Plex     []plex.Config     `yaml:"plex"`
	} `yaml:"targets"`
}

// Limits for the HTTP server. They stop a client that holds a connection
// open by sending request headers slowly. The server has no full-request
// read or write timeout, so a slow request body or a slow response still
// works.
const (
	// readHeaderTimeout is the time to read the headers of a request.
	readHeaderTimeout = 10 * time.Second

	// idleTimeout is the time to wait for the next request on a keep-alive
	// connection. Without it, the server waits with no limit for the first
	// bytes of the next request, before readHeaderTimeout starts.
	idleTimeout = 2 * time.Minute
)

var (
	// release variables
	Version   string
	Timestamp string
	GitCommit string

	// CLI
	cli struct {
		Version kong.VersionFlag `help:"Print version information and quit"`

		// An empty path means a file in the default config directory.
		// applyDefaultPaths resolves it after parsing, so a set path never
		// creates that directory.
		Config    string `type:"path" env:"AUTOSCAN_CONFIG" help:"Config file path (default: config.yml in the default config directory)"`
		Database  string `type:"path" env:"AUTOSCAN_DATABASE" help:"Database file path (default: autoscan.db in the default config directory)"`
		Log       string `type:"path" env:"AUTOSCAN_LOG" help:"Log file path (default: activity.log in the default config directory)"`
		Verbosity int    `type:"counter" default:"0" short:"v" env:"AUTOSCAN_VERBOSITY" help:"Log level verbosity"`
	}
)

func main() {
	version := fmt.Sprintf("%s (%s@%s)", Version, GitCommit, Timestamp)

	// parse cli. kong.Parse exits if the arguments are not valid.
	kong.Parse(&cli,
		kong.Name("autoscan"),
		kong.Description("Scan media into target media servers"),
		kong.UsageOnError(),
		kong.ConfigureHelp(kong.HelpOptions{
			Summary: true,
			Compact: true,
		}),
		kong.Vars{
			"version": version,
		},
	)

	applyDefaultPaths(&cli.Config, &cli.Database, &cli.Log)

	// logger
	logger := log.Output(io.MultiWriter(zerolog.ConsoleWriter{
		TimeFormat: time.Stamp,
		Out:        os.Stderr,
	}, zerolog.ConsoleWriter{
		TimeFormat: time.Stamp,
		Out: &lumberjack.Logger{
			Filename:   cli.Log,
			MaxSize:    5,
			MaxAge:     14,
			MaxBackups: 5,
		},
		NoColor: true,
	}))

	switch {
	case cli.Verbosity == 1:
		log.Logger = logger.Level(zerolog.DebugLevel)
	case cli.Verbosity > 1:
		log.Logger = logger.Level(zerolog.TraceLevel)
	default:
		log.Logger = logger.Level(zerolog.InfoLevel)
	}

	// datastore
	db, err := sql.Open("sqlite", cli.Database)
	if err != nil {
		log.Fatal().
			Err(err).
			Msg("Failed opening datastore")
	}
	db.SetMaxOpenConns(1)

	// config
	file, err := waitForConfig(cli.Config, 5*time.Second)
	if err != nil {
		log.Fatal().
			Err(err).
			Msg("Failed opening config")
	}
	defer file.Close()

	// set default values
	c := config{
		MinimumAge: 10 * time.Minute,
		ScanDelay:  5 * time.Second,
		ScanStats:  1 * time.Hour,
		Host:       []string{""},
		Port:       3030,
	}

	decoder := yaml.NewDecoder(file)
	decoder.SetStrict(true)
	err = decoder.Decode(&c)
	if err != nil {
		log.Fatal().
			Err(err).
			Msg("Failed decoding config")
	}

	// migrator
	mg, err := migrate.New(db, "migrations")
	if err != nil {
		log.Fatal().
			Err(err).
			Msg("Failed initialising migrator")
	}

	// targets
	targets := slices.Concat(
		newTargets("autoscan", c.Targets.Autoscan, func(t ast.Config) string { return t.URL }, ast.New),
		newTargets("plex", c.Targets.Plex, func(t plex.Config) string { return t.URL }, plex.New),
		newTargets("emby", c.Targets.Emby, func(t emby.Config) string { return t.URL }, emby.New),
		newTargets("jellyfin", c.Targets.Jellyfin, func(t jellyfin.Config) string { return t.URL }, jellyfin.New),
	)

	// processor
	proc, err := processor.New(processor.Config{
		Anchors:    c.Anchors,
		MinimumAge: c.MinimumAge,
		ScanDelay:  c.ScanDelay,
		Targets:    targets,
		Db:         db,
		Mg:         mg,
	})

	if err != nil {
		log.Fatal().
			Err(err).
			Msg("Failed initialising processor")
	}

	log.Info().
		Stringer("min_age", c.MinimumAge).
		Strs("anchors", c.Anchors).
		Msg("Initialised processor")

	// Check authentication. If no auth -> warn user.
	if c.Auth.Username == "" || c.Auth.Password == "" {
		log.Warn().Msg("Webhooks running without authentication")
	}

	// daemon triggers
	for _, t := range c.Triggers.Inotify {
		run, err := inotify.New(t, proc.Add)
		if err != nil {
			log.Fatal().
				Err(err).
				Str("trigger", "inotify").
				Msg("Failed initialising trigger")
		}

		go run()
	}

	// http triggers
	router := getRouter(c, proc)

	for _, h := range c.Host {
		go func(host string) {
			addr := host
			if !strings.Contains(addr, ":") {
				addr = fmt.Sprintf("%s:%d", host, c.Port)
			}

			log.Info().Msgf("Starting server on %s", addr)
			srv := &http.Server{
				Addr:              addr,
				Handler:           router,
				ReadHeaderTimeout: readHeaderTimeout,
				IdleTimeout:       idleTimeout,
			}
			if err := srv.ListenAndServe(); err != nil {
				log.Fatal().
					Str("addr", addr).
					Err(err).
					Msg("Failed starting web server")
			}
		}(h)
	}

	log.Info().
		Int("manual", 1).
		Int("inotify", len(c.Triggers.Inotify)).
		Int("lidarr", len(c.Triggers.Lidarr)).
		Int("radarr", len(c.Triggers.Radarr)).
		Int("readarr", len(c.Triggers.Readarr)).
		Int("sonarr", len(c.Triggers.Sonarr)).
		Msg("Initialised triggers")

	log.Info().
		Int("autoscan", len(c.Targets.Autoscan)).
		Int("plex", len(c.Targets.Plex)).
		Int("emby", len(c.Targets.Emby)).
		Int("jellyfin", len(c.Targets.Jellyfin)).
		Msg("Configured targets")

	// scan stats
	if c.ScanStats > 0 {
		go scanStats(proc, c.ScanStats)
	}

	// display initialised banner
	log.Info().
		Str("version", version).
		Msg("Initialised")

	// processor
	log.Info().Msg("Processor started")

	if len(targets) == 0 {
		log.Warn().Msg("No targets initialised, processor stopped, triggers will continue...")
		select {}
	}

	proc.Run(context.Background())
}

func newTargets[C any](kind string, configs []C, rawURL func(C) string, open func(C) (autoscan.Target, error)) []processor.Target {
	targets := make([]processor.Target, 0, len(configs))
	occurrences := make(map[string]int)
	for i, cfg := range configs {
		address := rawURL(cfg)
		occurrences[address]++
		// The URL can contain credentials. Only include its host and a digest
		// in the ID, and distinguish repeated entries for the same address.
		host := ""
		if parsed, err := url.Parse(address); err == nil {
			host = parsed.Host
		}
		id := fmt.Sprintf("%s:%s:%x:%d", kind, host, sha256.Sum256([]byte(address)), occurrences[address])
		targets = append(targets, processor.Target{
			ID:          id,
			StatusLabel: fmt.Sprintf("%s-%d", kind, i+1),
			Open: func() (autoscan.Target, error) {
				return open(cfg)
			},
		})
	}
	return targets
}
