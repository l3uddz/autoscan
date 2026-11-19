package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/alecthomas/kong"
	"github.com/natefinch/lumberjack"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

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

// shutdownTimeout limits how long autoscan waits for open requests and
// deliveries after a stop signal. Docker sends SIGKILL 10 seconds after
// SIGTERM by default. A delivery that does not finish stays queued.
const shutdownTimeout = 8 * time.Second

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
	log.Logger = newLogger(cli.Log, cli.Verbosity)

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

	c, err := decodeConfig(file)
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

	// Catch stop signals only after startup. Before this point, a stop
	// signal ends autoscan at once, for example while it waits for config.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// daemon triggers
	watchers := make([]*inotify.Trigger, 0, len(c.Triggers.Inotify))
	for _, t := range c.Triggers.Inotify {
		watcher, err := inotify.New(t, proc.Add)
		if err != nil {
			log.Fatal().
				Err(err).
				Str("trigger", "inotify").
				Msg("Failed initialising trigger")
		}

		watchers = append(watchers, watcher)
		go watcher.Run()
	}

	// http triggers
	router, err := getRouter(c, proc, watchers)
	if err != nil {
		log.Fatal().
			Err(err).
			Msg("Failed initialising trigger")
	}

	servers := startServers(c.Host, c.Port, router)
	logConfigured(c)

	// scan stats
	if c.ScanStats > 0 {
		go scanStats(proc, c.ScanStats)
	}

	// display initialised banner
	log.Info().
		Str("version", version).
		Msg("Initialised")

	if len(targets) == 0 {
		log.Warn().Msg("No targets initialised, processor stopped, triggers will continue...")
	} else {
		log.Info().Msg("Processor started")
	}

	processorDone := make(chan struct{})
	go func() {
		defer close(processorDone)
		proc.Run(ctx)
	}()

	<-ctx.Done()
	log.Info().Msg("Shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	shutdown(shutdownCtx, servers, processorDone)

	if err := db.Close(); err != nil {
		log.Error().Err(err).Msg("Failed closing datastore")
	}
	log.Info().Msg("Stopped")
}

// shutdown stops all servers at the same time, so no server accepts new
// requests while another waits for its open requests. Then it waits for the
// processor to stop. It waits at most until ctx ends.
func shutdown(ctx context.Context, servers []*http.Server, processorDone <-chan struct{}) {
	var stopped sync.WaitGroup
	for _, srv := range servers {
		stopped.Go(func() {
			if err := srv.Shutdown(ctx); err != nil {
				log.Warn().
					Str("addr", srv.Addr).
					Err(err).
					Msg("Failed stopping web server")
			}
		})
	}
	stopped.Wait()

	select {
	case <-processorDone:
	case <-ctx.Done():
		log.Warn().Msg("Processor did not stop in time, unfinished deliveries stay queued")
	}
}

// newLogger returns a logger that writes to stderr and to the rotated log
// file at path. verbosity 1 selects debug and more selects trace.
func newLogger(path string, verbosity int) zerolog.Logger {
	logger := log.Output(io.MultiWriter(zerolog.ConsoleWriter{
		TimeFormat: time.Stamp,
		Out:        os.Stderr,
	}, zerolog.ConsoleWriter{
		TimeFormat: time.Stamp,
		Out: &lumberjack.Logger{
			Filename:   path,
			MaxSize:    5,
			MaxAge:     14,
			MaxBackups: 5,
		},
		NoColor: true,
	}))

	switch {
	case verbosity == 1:
		return logger.Level(zerolog.DebugLevel)
	case verbosity > 1:
		return logger.Level(zerolog.TraceLevel)
	default:
		return logger.Level(zerolog.InfoLevel)
	}
}

// startServers serves handler on each host in the background and returns
// the servers. A host without a port uses port. Autoscan stops if a server
// fails.
func startServers(hosts []string, port int, handler http.Handler) []*http.Server {
	servers := make([]*http.Server, 0, len(hosts))
	for _, host := range hosts {
		addr := host
		if !strings.Contains(addr, ":") {
			addr = fmt.Sprintf("%s:%d", host, port)
		}

		srv := &http.Server{
			Addr:              addr,
			Handler:           handler,
			ReadHeaderTimeout: readHeaderTimeout,
			IdleTimeout:       idleTimeout,
		}
		servers = append(servers, srv)

		go func() {
			log.Info().Msgf("Starting server on %s", addr)
			if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
				log.Fatal().
					Str("addr", addr).
					Err(err).
					Msg("Failed starting web server")
			}
		}()
	}

	return servers
}

// logConfigured logs how many triggers and targets c configures.
func logConfigured(c config) {
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
