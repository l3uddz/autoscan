// Package webhook holds the HTTP handler that the webhook triggers share.
// Each trigger supplies a Parser that turns its request into an Event.
// The handler rewrites the folders, removes duplicates, and sends the scans
// to the processor.
package webhook

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/hlog"

	"github.com/l3uddz/autoscan"
)

// Config is the configuration of a Lidarr, Radarr, Readarr, or Sonarr trigger.
type Config struct {
	Name      string             `yaml:"name"`
	Priority  int                `yaml:"priority"`
	Rewrite   []autoscan.Rewrite `yaml:"rewrite"`
	Verbosity string             `yaml:"verbosity"`
}

// ErrMissingFields means that an event lacks a field that the trigger needs.
var ErrMissingFields = errors.New("required fields are missing")

// errRelativeFolder means that a folder is not an absolute path after the
// rewrite. A media server cannot find such a folder.
var errRelativeFolder = errors.New("folder is not an absolute path after the rewrite")

// An Event is what a trigger reads from one webhook request.
type Event struct {
	// Type is the event type, for logs. The -arrs send the type "Test"
	// when you test the webhook.
	Type string

	// Folders are the folders to scan, before the rewrite.
	// A Test event or an event that the trigger does not handle has none.
	Folders []string
}

// A Parser reads one webhook request. If it returns an error, the handler
// responds with 400 Bad Request.
type Parser func(r *http.Request) (Event, error)

// New returns the webhook handler for parse. It uses the priority, the
// rewrite rules, and the verbosity of c. The handler sends the scans to
// callback.
func New(c Config, parse Parser, callback autoscan.ProcessorFunc) (http.Handler, error) {
	rewriter, err := autoscan.NewRewriter(c.Rewrite)
	if err != nil {
		return nil, err
	}

	level, hasLevel := autoscan.ParseVerbosity(c.Verbosity)

	return handler{
		priority: c.Priority,
		rewrite:  rewriter,
		level:    level,
		hasLevel: hasLevel,
		parse:    parse,
		callback: callback,
	}, nil
}

type handler struct {
	priority int
	rewrite  autoscan.Rewriter
	level    zerolog.Level
	hasLevel bool
	parse    Parser
	callback autoscan.ProcessorFunc
}

func (h handler) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	r = h.withLogLevel(r)
	rlog := hlog.FromRequest(r)

	event, err := h.parse(r)
	if err != nil {
		rlog.Error().Err(err).Msg("Invalid request")
		rw.WriteHeader(http.StatusBadRequest)
		return
	}

	if strings.EqualFold(event.Type, "Test") {
		rlog.Info().Msg("Received test event")
		rw.WriteHeader(http.StatusOK)
		return
	}

	scans, err := h.scans(event.Folders)
	if err != nil {
		rlog.Error().Err(err).Msg("Invalid request")
		rw.WriteHeader(http.StatusBadRequest)
		return
	}
	if len(scans) == 0 {
		rlog.Debug().Str("event", event.Type).Msg("No folders to scan")
		rw.WriteHeader(http.StatusOK)
		return
	}

	if err := h.callback(scans...); err != nil {
		rlog.Error().Err(err).Msg("Processor could not process scans")
		rw.WriteHeader(http.StatusInternalServerError)
		return
	}

	for _, scan := range scans {
		rlog.Info().
			Str("path", scan.Folder).
			Str("event", event.Type).
			Msg("Scan moved to processor")
	}

	rw.WriteHeader(http.StatusOK)
}

// withLogLevel returns r with its request logger set to the verbosity of
// the trigger. The parser gets the same logger through hlog.FromRequest.
// If the trigger has no verbosity, r keeps the global log level.
func (h handler) withLogLevel(r *http.Request) *http.Request {
	if !h.hasLevel {
		return r
	}

	l := hlog.FromRequest(r).Level(h.level)
	return r.WithContext(l.WithContext(r.Context()))
}

// scans rewrites each folder and returns one scan for each unique result.
// It keeps the order of the folders. It skips a folder that is empty before
// or after the rewrite, so the processor never gets a scan for "". It
// returns an error if a folder is not an absolute path after the rewrite.
// The scans of one event have the same time.
func (h handler) scans(folders []string) ([]autoscan.Scan, error) {
	now := time.Now()
	seen := make(map[string]bool, len(folders))
	scans := make([]autoscan.Scan, 0, len(folders))

	for _, folder := range folders {
		if folder == "" {
			continue
		}

		folder = h.rewrite(folder)
		if folder == "" || seen[folder] {
			continue
		}
		if !absolutePath(folder) {
			return nil, fmt.Errorf("%w: %s", errRelativeFolder, folder)
		}

		seen[folder] = true
		scans = append(scans, autoscan.Scan{
			Folder:   folder,
			Priority: h.priority,
			Time:     now,
		})
	}

	return scans, nil
}

// maxBodyBytes is the largest webhook body that DecodeJSON reads.
// Real -arr payloads are a few kilobytes. A rename of a large series is
// far larger, so the limit leaves a wide margin.
const maxBodyBytes = 10 << 20 // 10 MiB

// DecodeJSON decodes the JSON body of r into v and logs v at trace level.
// If the body is larger than maxBodyBytes, DecodeJSON returns an error that
// wraps *http.MaxBytesError.
func DecodeJSON(r *http.Request, v any) error {
	body := http.MaxBytesReader(nil, r.Body, maxBodyBytes)
	if err := json.NewDecoder(body).Decode(v); err != nil {
		return fmt.Errorf("decoding request: %w", err)
	}

	// Decode stops after the first JSON value. Read the rest, so that a
	// small value with a large tail also fails.
	if _, err := io.Copy(io.Discard, body); err != nil {
		return fmt.Errorf("reading request: %w", err)
	}

	hlog.FromRequest(r).Trace().Interface("event", v).Msg("Received JSON body")
	return nil
}

// A File is one file in a Lidarr or Readarr event.
type File struct {
	Path string
}

// FileEvent returns the Event for Lidarr and Readarr. Both send a Download
// event with a list of files. A Download event without files is an error.
// Any other event type has no folders to scan.
func FileEvent(eventType string, files []File) (Event, error) {
	event := Event{Type: eventType}
	if !strings.EqualFold(eventType, "Download") {
		return event, nil
	}

	if len(files) == 0 {
		return event, ErrMissingFields
	}

	for _, f := range files {
		event.Folders = append(event.Folders, ParentFolder(f.Path))
	}

	return event, nil
}

// The -arrs send the paths of the system that they run on. On Windows, a
// path has a drive letter or a UNC prefix and can separate its folders with
// backslashes, which path.Dir and path.Join ignore. The helpers below keep
// the separators of the input, so that rewrite rules match each folder in
// the same form as the other paths of the event.

// windowsPath reports whether p is a Windows path with a drive letter or
// a UNC path, such as C:\TV or \\server\share.
func windowsPath(p string) bool {
	hasDrive := len(p) >= 2 && p[1] == ':' &&
		('a' <= p[0] && p[0] <= 'z' || 'A' <= p[0] && p[0] <= 'Z')
	return hasDrive || strings.HasPrefix(p, `\\`)
}

// absolutePath reports whether p is an absolute Unix or Windows path. A
// drive letter needs a separator after the colon, because C:TV and C: are
// relative to the current folder of drive C.
func absolutePath(p string) bool {
	if strings.HasPrefix(p, "/") || strings.HasPrefix(p, `\\`) {
		return true
	}
	return windowsPath(p) && len(p) > 2 && (p[2] == '\\' || p[2] == '/')
}

// CleanFolder cleans a Unix or Windows folder path while keeping its
// separator style and preserving Windows drive roots and UNC server/share roots.
func CleanFolder(dir string) string {
	if !windowsPath(dir) {
		return path.Clean(dir)
	}
	if strings.HasPrefix(dir, `\\`) {
		return cleanUNC(dir)
	}
	if len(dir) == 2 || (dir[2] != '/' && dir[2] != '\\') {
		return dir
	}
	cleaned := path.Clean(strings.ReplaceAll(dir[2:], `\`, "/"))
	if dir[2] == '\\' {
		cleaned = strings.ReplaceAll(cleaned, "/", `\`)
	}
	return dir[:2] + cleaned
}

// cleanUNC cleans the path suffix below a \\server\share UNC volume root so
// ".." cannot escape the share. It keeps forward slashes when the path after
// the leading "\\" uses only forward slashes.
func cleanUNC(dir string) string {
	sep := `\`
	if !strings.Contains(dir[2:], `\`) {
		sep = "/"
	}
	rest := strings.ReplaceAll(dir[2:], `\`, "/")
	server, afterServer, hasShare := strings.Cut(rest, "/")
	if server == "" || !hasShare {
		return dir
	}
	share, suffix, hasSuffix := strings.Cut(afterServer, "/")
	if share == "" {
		return dir
	}
	root := `\\` + server + sep + share
	if !hasSuffix || suffix == "" {
		return root
	}
	cleaned := path.Clean("/" + suffix)
	if cleaned == "/" {
		return root
	}
	if sep == `\` {
		cleaned = strings.ReplaceAll(cleaned, "/", `\`)
	}
	return root + cleaned
}

// ParentFolder returns the folder of the file at the absolute path file.
// It cleans a Unix path, as path.Dir does. A root folder, such as "/" or
// "C:\", keeps its separator. ParentFolder returns "" for a path without a
// folder.
func ParentFolder(file string) string {
	if !windowsPath(file) {
		if !strings.Contains(file, "/") {
			return ""
		}
		return path.Dir(file)
	}

	i := strings.LastIndexAny(file, `/\`)
	switch {
	case i < 0:
		return ""
	case strings.HasPrefix(file, `\\`) && i <= 1:
		return ""
	case i == 2 && file[1] == ':':
		return file[:i+1]
	default:
		return file[:i]
	}
}

// FileFolder returns the folder of the file at relative, a path below the
// folder base. An empty relative path returns the cleaned base folder.
// A Windows base joins relative with its own separator.
func FileFolder(base string, relative string) string {
	if !windowsPath(base) {
		if strings.TrimLeft(relative, "/") == "" {
			return CleanFolder(base)
		}
		return path.Dir(path.Join(base, relative))
	}

	separator := "/"
	if strings.Contains(base, `\`) {
		separator = `\`
	}
	relative = strings.TrimLeft(relative, `/\`)
	if relative == "" {
		return CleanFolder(base)
	}
	return ParentFolder(strings.TrimRight(base, `/\`) + separator + relative)
}
