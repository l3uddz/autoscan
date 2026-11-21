package emby

import (
	"context"

	"github.com/l3uddz/autoscan"
	"github.com/l3uddz/autoscan/targets/internal/media"
)

// Config is the configuration of a Emby target.
type Config = media.Config

// New connects to the Emby server of c and returns its target.
func New(ctx context.Context, c Config) (autoscan.Target, error) {
	l := media.Logger("emby", c)

	api, err := newAPIClient(c.URL, c.Token, l)
	if err != nil {
		return nil, err
	}

	return media.NewTarget(ctx, l, c, api)
}
