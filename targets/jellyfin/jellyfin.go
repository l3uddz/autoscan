package jellyfin

import (
	"net/url"

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
		Str("target", "jellyfin").
		Str("url", redactConfigURL(c.URL)).
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

// redactConfigURL returns the configured URL without the user password.
func redactConfigURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "invalid url"
	}

	return redactURL(u)
}
