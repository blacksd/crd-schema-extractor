package fetcher

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/blacksd/crd-schema-extractor/internal/source"
)

func TestParseGitHubURL(t *testing.T) {
	tests := []struct {
		name      string
		url       string
		wantOwner string
		wantRepo  string
		wantErr   bool
	}{
		{
			name:      "standard URL",
			url:       "https://github.com/crossplane/crossplane",
			wantOwner: "crossplane",
			wantRepo:  "crossplane",
		},
		{
			name:      "trailing slash",
			url:       "https://github.com/crossplane/crossplane/",
			wantOwner: "crossplane",
			wantRepo:  "crossplane",
		},
		{
			name:      ".git suffix",
			url:       "https://github.com/cilium/cilium.git",
			wantOwner: "cilium",
			wantRepo:  "cilium",
		},
		{
			name:    "non-GitHub host",
			url:     "https://gitlab.com/owner/repo",
			wantErr: true,
		},
		{
			name:    "missing repo path",
			url:     "https://github.com/owner",
			wantErr: true,
		},
		{
			name:      "extra path segments",
			url:       "https://github.com/org/repo/tree/main",
			wantOwner: "org",
			wantRepo:  "repo",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			owner, repo, err := parseGitHubURL(tt.url)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if owner != tt.wantOwner {
				t.Errorf("owner = %q, want %q", owner, tt.wantOwner)
			}
			if repo != tt.wantRepo {
				t.Errorf("repo = %q, want %q", repo, tt.wantRepo)
			}
		})
	}
}

// makeTarGz creates a tar.gz in memory with a single top-level directory
// containing the given files, mimicking GitHub's archive format.
func makeTarGz(t *testing.T, topDir string, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	// Top-level directory
	if err := tw.WriteHeader(&tar.Header{
		Name:     topDir + "/",
		Typeflag: tar.TypeDir,
		Mode:     0755,
	}); err != nil {
		t.Fatal(err)
	}

	for name, content := range files {
		fullPath := topDir + "/" + name
		// Create parent directories
		dir := filepath.Dir(fullPath)
		if dir != topDir {
			if err := tw.WriteHeader(&tar.Header{
				Name:     dir + "/",
				Typeflag: tar.TypeDir,
				Mode:     0755,
			}); err != nil {
				t.Fatal(err)
			}
		}
		if err := tw.WriteHeader(&tar.Header{
			Name:     fullPath,
			Size:     int64(len(content)),
			Typeflag: tar.TypeReg,
			Mode:     0644,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}

	tw.Close()
	gw.Close()
	return buf.Bytes()
}

func TestGitHubFetcherSuccess(t *testing.T) {
	tarball := makeTarGz(t, "myrepo-1.0.0", map[string]string{
		"crds/foo.yaml": "kind: CustomResourceDefinition",
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/owner/myrepo/tarball/v1.0.0" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("expected Authorization header")
		}
		w.Write(tarball)
	}))
	defer srv.Close()

	// Override the archive URL by using a custom client that redirects
	// api.github.com to our test server.
	f := &GitHubFetcher{
		Client: srv.Client(),
		Token:  "test-token",
	}

	// We need to intercept the URL construction. Instead, let's use a transport
	// that rewrites the host.
	transport := &rewriteTransport{
		base:    http.DefaultTransport,
		target:  srv.URL,
		origURL: "https://api.github.com",
	}
	f.Client = &http.Client{Transport: transport}

	result, err := f.Fetch(nopLog, source.Source{
		Name:    "test",
		Type:    "git+github",
		Repo:    "https://github.com/owner/myrepo",
		Version: "v1.0.0",
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	defer os.RemoveAll(result.Dir)

	if result.Dir == "" {
		t.Fatal("expected Dir to be set")
	}

	// Verify the hoisted structure: repo/crds/foo.yaml should exist
	fooPath := filepath.Join(result.Dir, "repo", "crds", "foo.yaml")
	data, err := os.ReadFile(fooPath)
	if err != nil {
		t.Fatalf("reading extracted file: %v", err)
	}
	if string(data) != "kind: CustomResourceDefinition" {
		t.Errorf("content = %q, want CRD marker", string(data))
	}
}

func TestGitHubFetcherHTTPError(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	f := &GitHubFetcher{
		Client: &http.Client{Transport: &rewriteTransport{
			base:    http.DefaultTransport,
			target:  srv.URL,
			origURL: "https://api.github.com",
		}},
		Token: "tok",
	}

	_, err := f.Fetch(nopLog, source.Source{
		Name:    "test-fail",
		Type:    "git+github",
		Repo:    "https://github.com/owner/repo",
		Version: "v1.0.0",
	})
	if err == nil {
		t.Fatal("expected error on 404")
	}
	if attempts.Load() != int32(maxRetries) {
		t.Errorf("expected %d attempts, got %d", maxRetries, attempts.Load())
	}
}

func TestGitHubFetcherInvalidRepoURL(t *testing.T) {
	f := &GitHubFetcher{}
	_, err := f.Fetch(nopLog, source.Source{
		Name:    "test-invalid",
		Type:    "git+github",
		Repo:    "https://gitlab.com/owner/repo",
		Version: "v1.0.0",
	})
	if err == nil {
		t.Fatal("expected error for non-GitHub URL")
	}
}

func TestHoistSingleChild(t *testing.T) {
	dir := t.TempDir()
	child := filepath.Join(dir, "repo-1.0.0")
	os.MkdirAll(filepath.Join(child, "subdir"), 0755)
	os.WriteFile(filepath.Join(child, "file.txt"), []byte("hello"), 0644)
	os.WriteFile(filepath.Join(child, "subdir", "nested.txt"), []byte("world"), 0644)

	if err := hoistSingleChild(dir); err != nil {
		t.Fatalf("hoistSingleChild: %v", err)
	}

	// Child directory should be gone
	if _, err := os.Stat(child); !os.IsNotExist(err) {
		t.Error("expected child directory to be removed")
	}

	// Files should be at top level
	data, err := os.ReadFile(filepath.Join(dir, "file.txt"))
	if err != nil || string(data) != "hello" {
		t.Errorf("file.txt content = %q, want %q", string(data), "hello")
	}
	data, err = os.ReadFile(filepath.Join(dir, "subdir", "nested.txt"))
	if err != nil || string(data) != "world" {
		t.Errorf("nested.txt content = %q, want %q", string(data), "world")
	}
}

// rewriteTransport rewrites requests from origURL to target for testing.
type rewriteTransport struct {
	base    http.RoundTripper
	target  string
	origURL string
}

func (t *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.URL.Scheme = "http"
	req.URL.Host = t.target[len("http://"):]
	return t.base.RoundTrip(req)
}
