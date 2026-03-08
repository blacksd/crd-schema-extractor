package fetcher

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"sigs.k8s.io/yaml"

	"github.com/blacksd/crd-schema-extractor/internal/source"
)

// HelmHTTPFetcher fetches Helm charts from HTTP chart repositories.
// It implements the Helm repo protocol: fetch index.yaml, resolve chart URL,
// download and extract the tarball.
type HelmHTTPFetcher struct {
	// Client allows injecting a custom HTTP client for testing.
	Client *http.Client
}

// indexFile is a minimal representation of a Helm repo index.yaml.
type indexFile struct {
	Entries map[string][]chartEntry `json:"entries" yaml:"entries"`
}

// chartEntry is a minimal representation of a chart version in index.yaml.
type chartEntry struct {
	Name    string   `json:"name" yaml:"name"`
	Version string   `json:"version" yaml:"version"`
	URLs    []string `json:"urls" yaml:"urls"`
}

func (f *HelmHTTPFetcher) Fetch(log zerolog.Logger, src source.Source) (*Result, error) {
	client := f.Client
	if client == nil {
		client = &http.Client{Timeout: httpTimeout}
	}

	// 1. Fetch and parse index.yaml
	indexURL := strings.TrimSuffix(src.Repo, "/") + "/index.yaml"
	log.Debug().Str("url", indexURL).Msg("fetching repo index")

	indexData, err := httpGet(client, indexURL)
	if err != nil {
		return nil, fmt.Errorf("fetching index from %s: %w", src.Repo, err)
	}

	var idx indexFile
	if err := yaml.Unmarshal(indexData, &idx); err != nil {
		return nil, fmt.Errorf("parsing index from %s: %w", src.Repo, err)
	}

	// 2. Find the chart version entry
	versions, ok := idx.Entries[src.Chart]
	if !ok {
		return nil, fmt.Errorf("chart %q not found in repo %s", src.Chart, src.Repo)
	}

	entry, err := findVersion(versions, src.Version)
	if err != nil {
		return nil, fmt.Errorf("version %s of chart %s not found in %s: %w", src.Version, src.Chart, src.Repo, err)
	}

	if len(entry.URLs) == 0 {
		return nil, fmt.Errorf("chart %s@%s has no download URLs", src.Chart, src.Version)
	}

	// 3. Resolve the chart download URL (may be relative to repo base)
	chartURL, err := resolveChartURL(src.Repo, entry.URLs[0])
	if err != nil {
		return nil, fmt.Errorf("resolving chart URL: %w", err)
	}

	// 4. Download the tarball
	log.Debug().Str("url", chartURL).Msg("downloading chart tarball")
	tarballData, err := httpGet(client, chartURL)
	if err != nil {
		return nil, fmt.Errorf("downloading chart %s@%s: %w", src.Chart, src.Version, err)
	}

	// 5. Extract to temp directory
	tmpDir, err := os.MkdirTemp("", "crd-schemas-helm-*")
	if err != nil {
		return nil, fmt.Errorf("creating temp dir: %w", err)
	}

	chartDir := filepath.Join(tmpDir, "chart")
	if err := os.MkdirAll(chartDir, 0755); err != nil {
		os.RemoveAll(tmpDir)
		return nil, fmt.Errorf("creating chart dir: %w", err)
	}

	if err := extractTarGz(tarballData, chartDir); err != nil {
		os.RemoveAll(tmpDir)
		return nil, fmt.Errorf("extracting chart %s@%s: %w", src.Chart, src.Version, err)
	}

	log.Debug().Str("dir", chartDir).Msg("chart extracted")
	return &Result{Dir: tmpDir}, nil
}

// findVersion looks up a chart version, applying the same v-prefix fallback
// as the old helm pull logic: try with v stripped first, then original.
func findVersion(versions []chartEntry, version string) (*chartEntry, error) {
	stripped := strings.TrimPrefix(version, "v")
	// Try stripped version first (most repos use versions without v prefix)
	for i := range versions {
		if versions[i].Version == stripped {
			return &versions[i], nil
		}
	}
	// Try original version string
	for i := range versions {
		if versions[i].Version == version {
			return &versions[i], nil
		}
	}
	return nil, fmt.Errorf("version %s not found (also tried %s)", version, stripped)
}

// resolveChartURL resolves a chart URL that may be relative to the repo base URL.
func resolveChartURL(repoURL, chartURL string) (string, error) {
	// If it's already absolute, return as-is
	parsed, err := url.Parse(chartURL)
	if err != nil {
		return "", err
	}
	if parsed.IsAbs() {
		return chartURL, nil
	}

	// Resolve relative to repo base
	base, err := url.Parse(strings.TrimSuffix(repoURL, "/") + "/")
	if err != nil {
		return "", err
	}
	return base.ResolveReference(parsed).String(), nil
}

// httpGet performs an HTTP GET with retry logic.
func httpGet(client *http.Client, url string) ([]byte, error) {
	var lastErr error

	for attempt := 1; attempt <= maxRetries; attempt++ {
		resp, err := client.Get(url)
		if err != nil {
			lastErr = fmt.Errorf("GET %s: %w", url, err)
			if attempt < maxRetries {
				time.Sleep(retryInterval)
			}
			continue
		}

		data, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = fmt.Errorf("reading response from %s: %w", url, err)
			if attempt < maxRetries {
				time.Sleep(retryInterval)
			}
			continue
		}

		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("GET %s: HTTP %d", url, resp.StatusCode)
			if attempt < maxRetries {
				time.Sleep(retryInterval)
			}
			continue
		}

		return data, nil
	}

	return nil, lastErr
}
