package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"slices"
	"strconv"
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

	if err := run(version); err != nil {
		log.Fatal().
			Err(err).
			Msg("Failed running autoscan")
	}
	log.Info().Msg("Stopped")
}

// run starts autoscan with the paths in cli and runs it until a stop
// signal arrives. It returns an error if autoscan cannot start.
func run(version string) error {
	// config and target validation happen before opening the datastore, so a
	// missing or invalid config does not create or hold open the database file.
	c, err := loadConfig(cli.Config)
	if err != nil {
		return err
	}

	targets, err := buildTargets(c)
	if err != nil {
		return err
	}

	// datastore
	db, err := openDatabase(context.Background(), cli.Database)
	if err != nil {
		return fmt.Errorf("open datastore: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			log.Error().Err(err).Msg("Failed closing datastore")
		}
	}()

	// migrator
	mg, err := migrate.New(db, "migrations")
	if err != nil {
		return fmt.Errorf("initialise migrator: %w", err)
	}

	// processor
	proc, err := processor.New(processor.Config{
		Anchors:    c.Anchors,
		MinimumAge: c.MinimumAge,
		ScanDelay:  c.ScanDelay,
		Targets:    targets,
		DB:         db,
		Migrator:   mg,
	})
	if err != nil {
		return fmt.Errorf("initialise processor: %w", err)
	}

	log.Info().
		Stringer("min_age", c.MinimumAge).
		Strs("anchors", c.Anchors).
		Msg("Initialised processor")

	// validateConfig already warns when only one of the two values is set.
	if c.Auth.Username == "" && c.Auth.Password == "" {
		log.Warn().Msg("Webhooks running without authentication")
	}

	// Validate and build all triggers before starting any background workers.
	watchers, err := newWatchers(c.Triggers.Inotify, proc.Add)
	if err != nil {
		return err
	}

	// http triggers
	router, err := getRouter(c, proc, watchers)
	if err != nil {
		return fmt.Errorf("initialise trigger: %w", err)
	}

	// Catch stop signals only after startup. Before this point, a stop
	// signal ends autoscan at once, for example while it waits for config.
	// A web server that fails also stops autoscan, and run returns its error.
	serving, serverFailed := context.WithCancelCause(context.Background())
	defer serverFailed(nil)
	ctx, stop := signal.NotifyContext(serving, os.Interrupt, syscall.SIGTERM)
	defer stop()

	servers, err := startServers(c.Host, c.Port, router, serverFailed)
	if err != nil {
		return fmt.Errorf("start web server: %w", err)
	}
	logConfigured(c)

	// display initialised banner
	log.Info().
		Str("version", version).
		Msg("Initialised")

	if len(targets) == 0 {
		log.Warn().Msg("No targets initialised, processor stopped, triggers will continue...")
	} else {
		log.Info().Msg("Processor started")
	}

	backgroundDone := startBackground(ctx, proc, watchers, c.ScanStats)

	<-ctx.Done()
	// The cause of serving is nil after a stop signal.
	serverErr := context.Cause(serving)
	log.Info().Err(serverErr).Msg("Shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	shutdown(shutdownCtx, servers, backgroundDone)
	return serverErr
}

func buildTargets(c config) ([]processor.Target, error) {
	autoscanTargets, autoscanErr := newTargets("autoscan", c.Targets.Autoscan, ast.New)
	plexTargets, plexErr := newTargets("plex", c.Targets.Plex, plex.New)
	embyTargets, embyErr := newTargets("emby", c.Targets.Emby, emby.New)
	jellyfinTargets, jellyfinErr := newTargets("jellyfin", c.Targets.Jellyfin, jellyfin.New)
	if err := errors.Join(autoscanErr, plexErr, embyErr, jellyfinErr); err != nil {
		return nil, fmt.Errorf("check targets: %w", err)
	}
	return slices.Concat(autoscanTargets, plexTargets, embyTargets, jellyfinTargets), nil
}

// newWatchers creates an inotify trigger for each config in configs without
// starting them, so startup failures do not leak watcher goroutines.
func newWatchers(configs []inotify.Config, callback autoscan.ProcessorFunc) ([]*inotify.Trigger, error) {
	watchers := make([]*inotify.Trigger, 0, len(configs))
	var errs []error
	for i, t := range configs {
		watcher, err := inotify.New(t, callback)
		if err != nil {
			errs = append(errs, fmt.Errorf("initialise inotify-%d: %w", i+1, err))
			continue
		}
		watchers = append(watchers, watcher)
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return watchers, nil
}

// startBackground starts the inotify triggers, scan stats logger, and
// processor, and returns a channel that closes when all of them exit.
func startBackground(ctx context.Context, proc *processor.Processor, watchers []*inotify.Trigger, statsInterval time.Duration) <-chan struct{} {
	var wg sync.WaitGroup
	for _, watcher := range watchers {
		wg.Go(func() {
			watcher.Run(ctx)
		})
	}
	if statsInterval > 0 {
		wg.Go(func() {
			scanStats(ctx, proc, statsInterval)
		})
	}
	wg.Go(func() {
		proc.Run(ctx)
	})

	done := make(chan struct{})
	go func() {
		defer close(done)
		wg.Wait()
	}()
	return done
}

// shutdown stops all servers at the same time, so no server accepts new
// requests while another waits for its open requests. Then it waits for the
// background workers to stop. It waits at most until ctx ends.
func shutdown(ctx context.Context, servers []*http.Server, workersDone <-chan struct{}) {
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
	case <-workersDone:
	case <-ctx.Done():
		log.Warn().Msg("Workers did not stop in time, unfinished deliveries stay queued")
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

// listenAddr returns the listen address for a host entry. An entry without
// a port, such as "127.0.0.1", "::1" or "[::1]", gets port.
func listenAddr(host string, port int) string {
	if _, _, err := net.SplitHostPort(host); err == nil {
		return host
	}
	return net.JoinHostPort(strings.Trim(host, "[]"), strconv.Itoa(port))
}

// startServers listens on each host and serves handler in the background.
// A host without a port uses port. It opens every listener before it serves,
// so it returns the error of a host that cannot listen, and then serves on
// no host. If a server fails later, it calls fail with the error.
func startServers(hosts []string, port int, handler http.Handler, fail func(error)) ([]*http.Server, error) {
	listeners := make([]net.Listener, 0, len(hosts))
	for _, host := range hosts {
		ln, err := net.Listen("tcp", listenAddr(host, port))
		if err != nil {
			for _, open := range listeners {
				open.Close()
			}
			return nil, err
		}
		listeners = append(listeners, ln)
	}

	servers := make([]*http.Server, 0, len(listeners))
	for _, ln := range listeners {
		srv := &http.Server{
			Addr:              ln.Addr().String(),
			Handler:           handler,
			ReadHeaderTimeout: readHeaderTimeout,
			IdleTimeout:       idleTimeout,
		}
		servers = append(servers, srv)

		go serve(srv, ln, fail)
	}

	return servers, nil
}

// serve serves srv on ln until srv shuts down. If srv fails, serve calls
// fail with the error.
func serve(srv *http.Server, ln net.Listener, fail func(error)) {
	log.Info().Msgf("Starting server on %s", srv.Addr)
	if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		fail(fmt.Errorf("web server on %s: %w", srv.Addr, err))
	}
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

// targetConfig is the configuration of one target.
type targetConfig interface {
	// BaseURL returns the configured URL. The target ID uses it.
	BaseURL() string

	// Validate returns an error for a value that cannot work.
	Validate() error
}

// newTargets returns a processor target for each configuration in configs.
// It checks every configuration first, so that a bad value stops autoscan
// at startup instead of failing each connection.
func newTargets[C targetConfig](kind string, configs []C, open func(context.Context, C) (autoscan.Target, error)) ([]processor.Target, error) {
	var errs []error
	for i, cfg := range configs {
		if err := cfg.Validate(); err != nil {
			errs = append(errs, fmt.Errorf("%s-%d: %w", kind, i+1, err))
		}
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}

	targets := make([]processor.Target, 0, len(configs))
	occurrences := make(map[string]int)
	for i, cfg := range configs {
		address := cfg.BaseURL()
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
			Open: func(ctx context.Context) (autoscan.Target, error) {
				return open(ctx, cfg)
			},
		})
	}
	return targets, nil
}
