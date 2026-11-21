// Package jellyfin is the target that starts a library scan of a folder on
// a Jellyfin server.
package jellyfin

import (
	"context"

	"github.com/l3uddz/autoscan"
	"github.com/l3uddz/autoscan/targets/internal/media"
)

// Config is the configuration of a Jellyfin target.
type Config = media.Config

// New connects to the Jellyfin server of c and returns its target.
func New(ctx context.Context, c Config) (autoscan.Target, error) {
	l := media.Logger("jellyfin", c, apiKeyParam)

	api, err := newAPIClient(c.URL, c.Token, l)
	if err != nil {
		return nil, err
	}

	return media.NewTarget(ctx, l, c, api)
}
