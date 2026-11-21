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
