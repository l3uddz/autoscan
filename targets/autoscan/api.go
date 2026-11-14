package autoscan

import (
	"fmt"
	"net/http"
	"net/url"

	"github.com/rs/zerolog"

	"github.com/l3uddz/autoscan"
)

type apiClient struct {
	client  *http.Client
	log     zerolog.Logger
	baseURL string
	user    string
	pass    string
}

func newAPIClient(baseURL string, user string, pass string, log zerolog.Logger) apiClient {
	return apiClient{
		client:  &http.Client{Timeout: autoscan.TargetTimeout},
		log:     log,
		baseURL: baseURL,
		user:    user,
		pass:    pass,
	}
}

func (c apiClient) do(req *http.Request) (*http.Response, error) {
	res, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%v: %w", err, autoscan.ErrTargetUnavailable)
	}

	if err := autoscan.ResponseError(res, "invalid basic auth"); err != nil {
		c.log.Trace().
			Stringer("request_url", res.Request.URL).
			Int("response_status", res.StatusCode).
			Msg("Request failed")

		return nil, err
	}

	return res, nil
}

func (c apiClient) Available() error {
	// create request
	req, err := http.NewRequest("HEAD", autoscan.JoinURL(c.baseURL, "triggers", "manual"), nil)
	if err != nil {
		return fmt.Errorf("failed creating head request: %v: %w", err, autoscan.ErrFatal)
	}

	if c.user != "" && c.pass != "" {
		req.SetBasicAuth(c.user, c.pass)
	}

	// send request
	res, err := c.do(req)
	if err != nil {
		return fmt.Errorf("availability: %w", err)
	}

	defer res.Body.Close()
	return nil
}

func (c apiClient) Scan(path string) error {
	// create request
	req, err := http.NewRequest("POST", autoscan.JoinURL(c.baseURL, "triggers", "manual"), nil)
	if err != nil {
		return fmt.Errorf("failed creating scan request: %v: %w", err, autoscan.ErrFatal)
	}

	if c.user != "" && c.pass != "" {
		req.SetBasicAuth(c.user, c.pass)
	}

	q := url.Values{}
	q.Add("dir", path)
	req.URL.RawQuery = q.Encode()

	// send request
	res, err := c.do(req)
	if err != nil {
		return fmt.Errorf("scan: %w", err)
	}

	defer res.Body.Close()
	return nil
}
