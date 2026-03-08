package fetcher

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/blacksd/crd-schema-extractor/internal/source"
)

// makeChartTarball creates a minimal Helm chart .tar.gz in memory.
func makeChartTarball(t *testing.T, chartName string, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	for name, content := range files {
		path := chartName + "/" + name
		hdr := &tar.Header{
			Name: path,
			Mode: 0644,
			Size: int64(len(content)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("writing tar header for %s: %v", path, err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatalf("writing tar content for %s: %v", path, err)
		}
	}

	tw.Close()
	gw.Close()
	return buf.Bytes()
}

func TestHelmHTTPFetcherSuccess(t *testing.T) {
	chartTarball := makeChartTarball(t, "my-chart", map[string]string{
		"Chart.yaml": "name: my-chart\nversion: 1.2.3\n",
		"crds/foo.yaml": `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: foos.example.com
`,
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index.yaml":
			w.Write([]byte(`apiVersion: v1
entries:
  my-chart:
    - name: my-chart
      version: "1.2.3"
      urls:
        - charts/my-chart-1.2.3.tgz
`))
		case "/charts/my-chart-1.2.3.tgz":
			w.Write(chartTarball)
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()

	f := &HelmHTTPFetcher{Client: srv.Client()}
	result, err := f.Fetch(nopLog, source.Source{
		Name:    "test",
		Type:    "helm",
		Repo:    srv.URL,
		Chart:   "my-chart",
		Version: "v1.2.3",
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	defer os.RemoveAll(result.Dir)

	if result.Dir == "" {
		t.Fatal("expected Dir to be set")
	}

	// Verify the chart was extracted
	crdPath := filepath.Join(result.Dir, "chart", "my-chart", "crds", "foo.yaml")
	if _, err := os.Stat(crdPath); err != nil {
		t.Errorf("expected CRD file at %s: %v", crdPath, err)
	}
}

func TestHelmHTTPFetcherAbsoluteURL(t *testing.T) {
	chartTarball := makeChartTarball(t, "my-chart", map[string]string{
		"Chart.yaml": "name: my-chart\nversion: 2.0.0\n",
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index.yaml":
			w.Write([]byte(`apiVersion: v1
entries:
  my-chart:
    - name: my-chart
      version: "2.0.0"
      urls:
        - ` + "http://" + r.Host + `/archive/my-chart-2.0.0.tgz
`))
		case "/archive/my-chart-2.0.0.tgz":
			w.Write(chartTarball)
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()

	f := &HelmHTTPFetcher{Client: srv.Client()}
	result, err := f.Fetch(nopLog, source.Source{
		Name:    "test",
		Type:    "helm",
		Repo:    srv.URL,
		Chart:   "my-chart",
		Version: "v2.0.0",
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	defer os.RemoveAll(result.Dir)

	chartYAML := filepath.Join(result.Dir, "chart", "my-chart", "Chart.yaml")
	if _, err := os.Stat(chartYAML); err != nil {
		t.Errorf("expected Chart.yaml at %s: %v", chartYAML, err)
	}
}

func TestHelmHTTPFetcherVersionFallback(t *testing.T) {
	chartTarball := makeChartTarball(t, "my-chart", map[string]string{
		"Chart.yaml": "name: my-chart\nversion: v1.0.0\n",
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index.yaml":
			// Index uses v-prefixed version
			w.Write([]byte(`apiVersion: v1
entries:
  my-chart:
    - name: my-chart
      version: "v1.0.0"
      urls:
        - charts/my-chart-v1.0.0.tgz
`))
		case "/charts/my-chart-v1.0.0.tgz":
			w.Write(chartTarball)
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()

	f := &HelmHTTPFetcher{Client: srv.Client()}
	result, err := f.Fetch(nopLog, source.Source{
		Name:    "test",
		Type:    "helm",
		Repo:    srv.URL,
		Chart:   "my-chart",
		Version: "v1.0.0",
	})
	if err != nil {
		t.Fatalf("Fetch with v-prefixed version: %v", err)
	}
	defer os.RemoveAll(result.Dir)
}

func TestHelmHTTPFetcherChartNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`apiVersion: v1
entries:
  other-chart:
    - name: other-chart
      version: "1.0.0"
      urls:
        - charts/other-chart-1.0.0.tgz
`))
	}))
	defer srv.Close()

	f := &HelmHTTPFetcher{Client: srv.Client()}
	_, err := f.Fetch(nopLog, source.Source{
		Name:    "test",
		Type:    "helm",
		Repo:    srv.URL,
		Chart:   "my-chart",
		Version: "v1.0.0",
	})
	if err == nil {
		t.Fatal("expected error for missing chart")
	}
}

func TestHelmHTTPFetcherVersionNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`apiVersion: v1
entries:
  my-chart:
    - name: my-chart
      version: "1.0.0"
      urls:
        - charts/my-chart-1.0.0.tgz
`))
	}))
	defer srv.Close()

	f := &HelmHTTPFetcher{Client: srv.Client()}
	_, err := f.Fetch(nopLog, source.Source{
		Name:    "test",
		Type:    "helm",
		Repo:    srv.URL,
		Chart:   "my-chart",
		Version: "v9.9.9",
	})
	if err == nil {
		t.Fatal("expected error for missing version")
	}
}

func TestHelmHTTPFetcherIndexFetchFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()

	f := &HelmHTTPFetcher{Client: srv.Client()}
	_, err := f.Fetch(nopLog, source.Source{
		Name:    "test",
		Type:    "helm",
		Repo:    srv.URL,
		Chart:   "my-chart",
		Version: "v1.0.0",
	})
	if err == nil {
		t.Fatal("expected error when index fetch fails")
	}
}
