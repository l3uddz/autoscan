package emby

import (
	"github.com/l3uddz/autoscan"
	"github.com/l3uddz/autoscan/targets/internal/media"
)

type Config struct {
	URL       string             `yaml:"url"`
	Token     string             `yaml:"token"`
	Rewrite   []autoscan.Rewrite `yaml:"rewrite"`
	Verbosity string             `yaml:"verbosity"`
}

func New(c Config) (autoscan.Target, error) {
	l := autoscan.GetLogger(c.Verbosity).With().
		Str("target", "emby").
		Str("url", c.URL).
		Logger()

	rewriter, err := autoscan.NewRewriter(c.Rewrite)
	if err != nil {
		return nil, err
	}

	api := newAPIClient(c.URL, c.Token, l)

	libraries, err := api.Libraries()
	if err != nil {
		return nil, err
	}

	l.Debug().
		Interface("libraries", libraries).
		Msg("Retrieved libraries")

	return media.NewPathTarget(l, rewriter, libraries, api), nil
}
