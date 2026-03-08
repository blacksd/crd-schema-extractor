package fetcher

import (
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/rs/zerolog"

	"github.com/blacksd/crd-schema-extractor/internal/source"
)

const (
	httpTimeout   = 60 * time.Second
	maxRetries    = 3
	retryInterval = 2 * time.Second
)

// URLFetcher fetches CRD manifests via HTTP GET with retry.
type URLFetcher struct {
	// Client allows injecting a custom HTTP client for testing.
	// If nil, a default client with httpTimeout is used.
	Client *http.Client
}

func (f *URLFetcher) Fetch(log zerolog.Logger, src source.Source) (*Result, error) {
	client := f.Client
	if client == nil {
		client = &http.Client{Timeout: httpTimeout}
	}

	var resp *http.Response
	var lastErr error

	for attempt := 1; attempt <= maxRetries; attempt++ {
		log.Debug().Str("url", src.URL).Int("attempt", attempt).Msg("fetching manifest")

		var err error
		resp, err = client.Get(src.URL)
		if err != nil {
			lastErr = fmt.Errorf("downloading %s: %w", src.URL, err)
			log.Warn().Err(err).Int("attempt", attempt).Int("max", maxRetries).Msg("fetch failed")
			if attempt < maxRetries {
				time.Sleep(retryInterval)
			}
			continue
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			lastErr = fmt.Errorf("downloading %s: HTTP %d", src.URL, resp.StatusCode)
			log.Warn().Int("status", resp.StatusCode).Int("attempt", attempt).Int("max", maxRetries).Msg("fetch returned non-200")
			if attempt < maxRetries {
				time.Sleep(retryInterval)
			}
			continue
		}

		lastErr = nil
		break
	}

	if lastErr != nil {
		return nil, lastErr
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response from %s: %w", src.URL, err)
	}

	log.Debug().Str("url", src.URL).Int("bytes", len(data)).Msg("manifest downloaded")
	return &Result{Data: data}, nil
}
