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

	"github.com/blacksd/crd-schema-extractor/internal/source"
)

// GitHubFetcher downloads a repository archive from GitHub at a specific tag.
// It uses the GitHub API tarball endpoint, which returns a redirect to a
// temporary download URL. Authentication via GITHUB_TOKEN is supported.
type GitHubFetcher struct {
	// Client allows injecting a custom HTTP client for testing.
	// If nil, a default client with httpTimeout is used.
	Client *http.Client

	// Token is the GitHub API token for authenticated requests.
	// If empty, the GITHUB_TOKEN environment variable is used.
	Token string
}

func (f *GitHubFetcher) Fetch(log zerolog.Logger, src source.Source) (*Result, error) {
	client := f.Client
	if client == nil {
		client = &http.Client{Timeout: httpTimeout}
	}

	owner, repo, err := parseGitHubURL(src.Repo)
	if err != nil {
		return nil, fmt.Errorf("parsing repo URL for %s: %w", src.Name, err)
	}

	archiveURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/tarball/%s", owner, repo, src.Version)
	log.Debug().Str("url", archiveURL).Msg("downloading git archive")

	token := f.Token
	if token == "" {
		token = os.Getenv("GITHUB_TOKEN")
	}

	tarballData, err := httpGetWithAuth(client, archiveURL, token)
	if err != nil {
		return nil, fmt.Errorf("downloading archive for %s@%s: %w", src.Name, src.Version, err)
	}

	tmpDir, err := os.MkdirTemp("", "crd-schemas-git-*")
	if err != nil {
		return nil, fmt.Errorf("creating temp dir: %w", err)
	}

	repoDir := filepath.Join(tmpDir, "repo")
	if err := os.MkdirAll(repoDir, 0755); err != nil {
		os.RemoveAll(tmpDir)
		return nil, fmt.Errorf("creating repo dir: %w", err)
	}

	if err := extractTarGz(tarballData, repoDir); err != nil {
		os.RemoveAll(tmpDir)
		return nil, fmt.Errorf("extracting archive for %s@%s: %w", src.Name, src.Version, err)
	}

	// GitHub tarballs have a single top-level directory (e.g., "crossplane-1.17.2/").
	// Hoist its contents up to repoDir so paths are predictable.
	if err := hoistSingleChild(repoDir); err != nil {
		os.RemoveAll(tmpDir)
		return nil, fmt.Errorf("normalizing archive structure for %s: %w", src.Name, err)
	}

	log.Debug().Str("dir", repoDir).Msg("git archive extracted")
	return &Result{Dir: tmpDir}, nil
}

// parseGitHubURL extracts owner and repo from a GitHub URL.
// Accepts formats like:
//   - https://github.com/owner/repo
//   - https://github.com/owner/repo.git
//   - https://github.com/owner/repo/
func parseGitHubURL(repoURL string) (owner, repo string, err error) {
	u, err := url.Parse(strings.TrimSuffix(repoURL, ".git"))
	if err != nil {
		return "", "", fmt.Errorf("invalid URL %q: %w", repoURL, err)
	}

	if u.Host != "github.com" {
		return "", "", fmt.Errorf("expected github.com host, got %q", u.Host)
	}

	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 2 {
		return "", "", fmt.Errorf("expected github.com/owner/repo, got %q", repoURL)
	}

	return parts[0], parts[1], nil
}

// httpGetWithAuth performs an HTTP GET with optional Bearer token authentication
// and retry logic.
func httpGetWithAuth(client *http.Client, rawURL, token string) ([]byte, error) {
	var lastErr error

	for attempt := 1; attempt <= maxRetries; attempt++ {
		req, err := http.NewRequest(http.MethodGet, rawURL, nil)
		if err != nil {
			return nil, fmt.Errorf("creating request for %s: %w", rawURL, err)
		}

		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		req.Header.Set("Accept", "application/vnd.github+json")

		resp, err := client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("GET %s: %w", rawURL, err)
			if attempt < maxRetries {
				time.Sleep(retryInterval)
			}
			continue
		}

		data, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = fmt.Errorf("reading response from %s: %w", rawURL, err)
			if attempt < maxRetries {
				time.Sleep(retryInterval)
			}
			continue
		}

		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("GET %s: HTTP %d", rawURL, resp.StatusCode)
			if attempt < maxRetries {
				time.Sleep(retryInterval)
			}
			continue
		}

		return data, nil
	}

	return nil, lastErr
}

// hoistSingleChild moves the contents of a single child directory up into the
// parent. This normalizes GitHub's tarball structure where everything is nested
// under a "{repo}-{version}/" directory.
func hoistSingleChild(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}

	// Find the single directory entry
	var child string
	for _, e := range entries {
		if e.IsDir() {
			if child != "" {
				return nil
			}
			child = e.Name()
		}
	}
	if child == "" {
		return nil
	}

	childDir := filepath.Join(dir, child)
	childEntries, err := os.ReadDir(childDir)
	if err != nil {
		return err
	}

	for _, e := range childEntries {
		from := filepath.Join(childDir, e.Name())
		to := filepath.Join(dir, e.Name())
		if err := os.Rename(from, to); err != nil {
			return fmt.Errorf("moving %s to %s: %w", from, to, err)
		}
	}

	return os.Remove(childDir)
}
