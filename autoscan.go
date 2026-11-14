package autoscan

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"time"
)

// A Scan is at the core of Autoscan.
// It defines which path to scan and with which (trigger-given) priority.
//
// The Scan is used across Triggers, Targets and the Processor.
type Scan struct {
	Folder   string
	Priority int
	Time     time.Time
}

type ProcessorFunc func(...Scan) error

type Trigger func(ProcessorFunc)

// A HTTPTrigger is a Trigger which does not run in the background,
// and instead returns a http.Handler.
//
// This http.Handler should be added to the autoscan router in cmd/autoscan.
type HTTPTrigger func(ProcessorFunc) http.Handler

// A Target receives a Scan from the Processor and translates the Scan
// into a format understood by the target.
type Target interface {
	Scan(Scan) error
	Available() error
}

var (
	// ErrTargetUnavailable may occur when a Target goes offline
	// or suffers from fatal errors. In this case, the processor
	// will halt operations until the target is back online.
	ErrTargetUnavailable = errors.New("target unavailable")

	// ErrFatal indicates a severe problem related to development.
	ErrFatal = errors.New("fatal error")

	// ErrNoScans is not an error. It only indicates whether the CLI
	// should sleep longer depending on the processor output.
	ErrNoScans = errors.New("no scans currently available")

	// ErrAnchorUnavailable indicates that an Anchor file is
	// not available on the file system. Processing should halt
	// until all anchors are available.
	ErrAnchorUnavailable = errors.New("anchor file is unavailable")
)

type Rewrite struct {
	From string `yaml:"from"`
	To   string `yaml:"to"`
}

type Rewriter func(string) string

// NewRewriter returns a Rewriter that applies the first rule whose From
// pattern matches the input. If no rule matches, it returns the input.
func NewRewriter(rewriteRules []Rewrite) (Rewriter, error) {
	patterns := make([]string, 0, len(rewriteRules))
	for _, rule := range rewriteRules {
		patterns = append(patterns, rule.From)
	}

	rewrites, err := compilePatterns("rewrite", patterns)
	if err != nil {
		return nil, err
	}

	rewriter := func(input string) string {
		for i, re := range rewrites {
			if re.MatchString(input) {
				return re.ReplaceAllString(input, rewriteRules[i].To)
			}
		}

		return input
	}

	return rewriter, nil
}

type Filterer func(string) bool

// NewFilterer returns a Filterer that rejects a path that matches an exclude.
// If includes are set, it also rejects a path that matches no include.
func NewFilterer(includes []string, excludes []string) (Filterer, error) {
	reIncludes, err := compilePatterns("include", includes)
	if err != nil {
		return nil, err
	}

	reExcludes, err := compilePatterns("exclude", excludes)
	if err != nil {
		return nil, err
	}

	filterer := func(path string) bool {
		if matchesAny(reExcludes, path) {
			return false
		}

		return len(reIncludes) == 0 || matchesAny(reIncludes, path)
	}

	return filterer, nil
}

// compilePatterns compiles each pattern. kind names the patterns in the error.
func compilePatterns(kind string, patterns []string) ([]*regexp.Regexp, error) {
	compiled := make([]*regexp.Regexp, 0, len(patterns))
	for _, pattern := range patterns {
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("compiling %s: %v: %w", kind, pattern, err)
		}

		compiled = append(compiled, re)
	}

	return compiled, nil
}

func matchesAny(patterns []*regexp.Regexp, input string) bool {
	for _, re := range patterns {
		if re.MatchString(input) {
			return true
		}
	}

	return false
}
