package plex

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/rs/zerolog"

	"github.com/l3uddz/autoscan"
	"github.com/l3uddz/autoscan/targets/internal/httpapi"
	"github.com/l3uddz/autoscan/targets/internal/media"
)

// Config is the configuration of a Plex target.
type Config = media.Config

type target struct {
	libraries *media.LibraryList

	log     zerolog.Logger
	rewrite autoscan.Rewriter
	api     apiClient
}

func New(ctx context.Context, c Config) (autoscan.Target, error) {
	l := autoscan.GetLogger(c.Verbosity).With().
		Str("target", "plex").
		Str("url", httpapi.RedactURL(c.URL)).Logger()

	rewriter, err := autoscan.NewRewriter(c.Rewrite)
	if err != nil {
		return nil, err
	}

	api, err := newAPIClient(c.URL, c.Token, l)
	if err != nil {
		return nil, err
	}

	version, err := api.Version(ctx)
	if err != nil {
		return nil, err
	}

	l.Debug().Msgf("Plex version: %s", version)
	if !isSupportedVersion(version) {
		return nil, fmt.Errorf("plex running unsupported version %s", version)
	}

	libraries, err := media.NewLibraryList(ctx, l, api.Libraries)
	if err != nil {
		return nil, err
	}

	return &target{
		libraries: libraries,

		log:     l,
		rewrite: rewriter,
		api:     api,
	}, nil
}

func (t *target) Available(ctx context.Context) error {
	_, err := t.api.Version(ctx)
	return err
}

func (t *target) Scan(ctx context.Context, scan autoscan.Scan) error {
	// determine library for this scan
	scanFolder := t.rewrite(scan.Folder)

	libs, err := t.libraries.Matching(ctx, scanFolder)
	if err != nil {
		return err
	}
	if len(libs) == 0 {
		return fmt.Errorf("%s: %w", scanFolder, autoscan.ErrNoLibrary)
	}

	// send scan request
	for _, lib := range libs {
		l := t.log.With().
			Str("path", scanFolder).
			Str("library", lib.Name).
			Logger()

		l.Trace().Msg("Sending scan request")

		if err := t.api.Scan(ctx, scanFolder, lib.ID); err != nil {
			return err
		}

		l.Info().Msg("Scan moved to target")
	}

	return nil
}

func isSupportedVersion(version string) bool {
	parts := strings.Split(version, ".")
	if len(parts) < 2 {
		return false
	}

	major, _ := strconv.Atoi(parts[0])
	minor, _ := strconv.Atoi(parts[1])

	if major >= 2 || (major == 1 && minor >= 20) {
		return true
	}

	return false
}
