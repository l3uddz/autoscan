// Package autoscan is the target that sends each scan to another autoscan
// instance.
package autoscan

import (
	"context"
	"errors"

	"github.com/rs/zerolog"

	"github.com/l3uddz/autoscan"
	"github.com/l3uddz/autoscan/targets/internal/httpapi"
)

type Config struct {
	URL       string             `yaml:"url"`
	User      string             `yaml:"username"`
	Pass      string             `yaml:"password"`
	Rewrite   []autoscan.Rewrite `yaml:"rewrite"`
	Verbosity string             `yaml:"verbosity"`
}

// BaseURL returns the configured URL of the other autoscan.
func (c Config) BaseURL() string {
	return c.URL
}

// Validate returns an error for each value of c that cannot work.
func (c Config) Validate() error {
	_, rewriteErr := autoscan.NewRewriter(c.Rewrite)
	return errors.Join(httpapi.CheckBaseURL(c.URL), rewriteErr)
}

type target struct {
	log     zerolog.Logger
	rewrite autoscan.Rewriter
	api     apiClient
}

func New(_ context.Context, c Config) (autoscan.Target, error) {
	l := autoscan.GetLogger(c.Verbosity).With().
		Str("target", "autoscan").
		Str("url", httpapi.RedactURL(c.URL)).Logger()

	rewriter, err := autoscan.NewRewriter(c.Rewrite)
	if err != nil {
		return nil, err
	}

	api, err := newAPIClient(c.URL, c.User, c.Pass, l)
	if err != nil {
		return nil, err
	}

	return &target{
		log:     l,
		rewrite: rewriter,
		api:     api,
	}, nil
}

func (t target) Scan(ctx context.Context, scan autoscan.Scan) error {
	scanFolder := t.rewrite(scan.Folder)

	// send scan request
	l := t.log.With().
		Str("path", scanFolder).
		Logger()

	l.Trace().Msg("Sending scan request")

	if err := t.api.Scan(ctx, scanFolder); err != nil {
		return err
	}

	l.Info().Msg("Scan moved to target")
	return nil
}

func (t target) Available(ctx context.Context) error {
	return t.api.Available(ctx)
}
