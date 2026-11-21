package jellyfin

import (
	"context"

	"github.com/l3uddz/autoscan"
	"github.com/l3uddz/autoscan/targets/internal/httpapi"
	"github.com/l3uddz/autoscan/targets/internal/media"
)

type Config struct {
	URL       string             `yaml:"url"`
	Token     string             `yaml:"token"`
	Rewrite   []autoscan.Rewrite `yaml:"rewrite"`
	Verbosity string             `yaml:"verbosity"`
}

func New(ctx context.Context, c Config) (autoscan.Target, error) {
	l := autoscan.GetLogger(c.Verbosity).With().
		Str("target", "jellyfin").
		Str("url", httpapi.RedactURL(c.URL, apiKeyParam)).
		Logger()

	rewriter, err := autoscan.NewRewriter(c.Rewrite)
	if err != nil {
		return nil, err
	}

	api, err := newAPIClient(c.URL, c.Token, l)
	if err != nil {
		return nil, err
	}

	libraries, err := media.NewLibraryList(ctx, l, api.Libraries)
	if err != nil {
		return nil, err
	}

	return media.NewPathTarget(l, rewriter, libraries, api), nil
}
