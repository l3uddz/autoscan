// Package autoscan holds the core types of autoscan: the scan, the
// processor callback of the triggers, the target interface, and the path
// rewriters and filters.
package autoscan

import (
	"context"
	"errors"
	"fmt"
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

// A ProcessorFunc receives the scans of a trigger.
type ProcessorFunc func(...Scan) error

// A Target receives a Scan from the Processor and translates the Scan
// into a format understood by the target. ctx ends a request when
// autoscan stops.
type Target interface {
	Scan(ctx context.Context, scan Scan) error
	Available(ctx context.Context) error
}

// ErrTargetUnavailable indicates that a target cannot accept scans.
var ErrTargetUnavailable = errors.New("target unavailable")

// ErrNoLibrary means that the scan folder is in no library of the target.
// With several targets, this is normal for a folder that only some of them
// hold. The processor removes the delivery without counting a processed
// scan.
var ErrNoLibrary = errors.New("folder is in no library of the target")

type Rewrite struct {
	From string `yaml:"from"`
	To   string `yaml:"to"`
}

type Rewriter func(string) string

type compiledRewrite struct {
	from *regexp.Regexp
	to   string
}

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

	rules := make([]compiledRewrite, len(rewrites))
	for i, re := range rewrites {
		rules[i] = compiledRewrite{from: re, to: rewriteRules[i].To}
	}

	rewriter := func(input string) string {
		for _, rule := range rules {
			if rule.from.MatchString(input) {
				return rule.from.ReplaceAllString(input, rule.to)
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
