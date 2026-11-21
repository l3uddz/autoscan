// Package plex is the target that starts a library scan of a folder on a
// Plex server.
package plex

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/l3uddz/autoscan"
	"github.com/l3uddz/autoscan/targets/internal/media"
)

// Config is the configuration of a Plex target.
type Config = media.Config

// New connects to the Plex server of c and returns its target. It returns an
// error for a Plex version older than 1.20.
func New(ctx context.Context, c Config) (autoscan.Target, error) {
	l := media.Logger("plex", c)

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

	return media.NewTarget(ctx, l, c, api)
}

// isSupportedVersion reports whether version is Plex 1.20 or later. A
// version whose major or minor part is not a number is not supported.
func isSupportedVersion(version string) bool {
	majorText, rest, _ := strings.Cut(version, ".")
	minorText, _, _ := strings.Cut(rest, ".")

	major, majorErr := strconv.Atoi(majorText)
	minor, minorErr := strconv.Atoi(minorText)
	if majorErr != nil || minorErr != nil {
		return false
	}

	return major > 1 || major == 1 && minor >= 20
}
