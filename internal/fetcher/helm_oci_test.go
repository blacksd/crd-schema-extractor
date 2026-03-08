package fetcher

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/blacksd/crd-schema-extractor/internal/source"
)

// mockOCIRepo implements OCIRepository for testing.
type mockOCIRepo struct {
	tags  map[string]ocispec.Descriptor // tag -> manifest descriptor
	blobs map[digest.Digest][]byte      // digest -> content
}

func (m *mockOCIRepo) Resolve(_ context.Context, reference string) (ocispec.Descriptor, error) {
	desc, ok := m.tags[reference]
	if !ok {
		return ocispec.Descriptor{}, fmt.Errorf("tag %q not found", reference)
	}
	return desc, nil
}

func (m *mockOCIRepo) Fetch(_ context.Context, target ocispec.Descriptor) (io.ReadCloser, error) {
	data, ok := m.blobs[target.Digest]
	if !ok {
		return nil, fmt.Errorf("blob %s not found", target.Digest)
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

// buildMockOCIRepo creates a mockOCIRepo serving a chart tarball at the given tag.
func buildMockOCIRepo(t *testing.T, tag string, chartName string, chartFiles map[string]string) *mockOCIRepo {
	t.Helper()

	// Build chart tarball
	var tarBuf bytes.Buffer
	gw := gzip.NewWriter(&tarBuf)
	tw := tar.NewWriter(gw)
	for name, content := range chartFiles {
		path := chartName + "/" + name
		hdr := &tar.Header{Name: path, Mode: 0644, Size: int64(len(content))}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("tar header: %v", err)
		}
		tw.Write([]byte(content))
	}
	tw.Close()
	gw.Close()
	chartData := tarBuf.Bytes()

	chartDigest := digest.FromBytes(chartData)
	chartLayer := ocispec.Descriptor{
		MediaType: helmChartMediaType,
		Digest:    chartDigest,
		Size:      int64(len(chartData)),
	}

	// Build config blob
	configData := []byte(`{"name":"` + chartName + `"}`)
	configDigest := digest.FromBytes(configData)
	configDesc := ocispec.Descriptor{
		MediaType: "application/vnd.cncf.helm.config.v1+json",
		Digest:    configDigest,
		Size:      int64(len(configData)),
	}

	// Build manifest
	manifest := ocispec.Manifest{
		MediaType: ocispec.MediaTypeImageManifest,
		Config:    configDesc,
		Layers:    []ocispec.Descriptor{chartLayer},
	}
	manifestData, _ := json.Marshal(manifest)
	manifestDigest := digest.FromBytes(manifestData)
	manifestDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    manifestDigest,
		Size:      int64(len(manifestData)),
	}

	return &mockOCIRepo{
		tags: map[string]ocispec.Descriptor{
			tag: manifestDesc,
		},
		blobs: map[digest.Digest][]byte{
			manifestDigest: manifestData,
			chartDigest:    chartData,
			configDigest:   configData,
		},
	}
}

func TestHelmOCIFetcherSuccess(t *testing.T) {
	mock := buildMockOCIRepo(t, "1.2.3", "my-chart", map[string]string{
		"Chart.yaml":    "name: my-chart\nversion: 1.2.3\n",
		"crds/foo.yaml": "kind: CustomResourceDefinition\n",
	})

	f := &HelmOCIFetcher{
		NewRepo: func(ref string) (OCIRepository, error) {
			if ref != "ghcr.io/org/charts/my-chart" {
				t.Errorf("unexpected ref: %s", ref)
			}
			return mock, nil
		},
	}

	result, err := f.Fetch(nopLog, source.Source{
		Name:    "test-oci",
		Type:    "helm",
		Repo:    "oci://ghcr.io/org/charts",
		Chart:   "my-chart",
		Version: "v1.2.3",
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	defer os.RemoveAll(result.Dir)

	// Verify extraction
	crdPath := filepath.Join(result.Dir, "chart", "my-chart", "crds", "foo.yaml")
	if _, err := os.Stat(crdPath); err != nil {
		t.Errorf("expected CRD at %s: %v", crdPath, err)
	}
}

func TestHelmOCIFetcherVersionFallback(t *testing.T) {
	// Register with v-prefixed tag (some registries use this)
	mock := buildMockOCIRepo(t, "v1.0.0", "my-chart", map[string]string{
		"Chart.yaml": "name: my-chart\nversion: v1.0.0\n",
	})

	f := &HelmOCIFetcher{
		NewRepo: func(ref string) (OCIRepository, error) {
			return mock, nil
		},
	}

	result, err := f.Fetch(nopLog, source.Source{
		Name:    "test-fallback",
		Type:    "helm",
		Repo:    "oci://ghcr.io/org/charts",
		Chart:   "my-chart",
		Version: "v1.0.0",
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	defer os.RemoveAll(result.Dir)
}

func TestHelmOCIFetcherTagNotFound(t *testing.T) {
	mock := &mockOCIRepo{
		tags:  map[string]ocispec.Descriptor{},
		blobs: map[digest.Digest][]byte{},
	}

	f := &HelmOCIFetcher{
		NewRepo: func(ref string) (OCIRepository, error) {
			return mock, nil
		},
	}

	_, err := f.Fetch(nopLog, source.Source{
		Name:    "test-notfound",
		Type:    "helm",
		Repo:    "oci://ghcr.io/org/charts",
		Chart:   "my-chart",
		Version: "v9.9.9",
	})
	if err == nil {
		t.Fatal("expected error for missing tag")
	}
}

func TestHelmOCIFetcherLegacyMediaType(t *testing.T) {
	// Build a mock that uses the legacy media type
	mock := buildMockOCIRepo(t, "1.0.0", "legacy-chart", map[string]string{
		"Chart.yaml": "name: legacy-chart\nversion: 1.0.0\n",
	})

	// Replace chart layer media type with legacy
	for tag, desc := range mock.tags {
		manifestData := mock.blobs[desc.Digest]
		var manifest ocispec.Manifest
		json.Unmarshal(manifestData, &manifest)
		manifest.Layers[0].MediaType = helmLegacyMediaType
		newManifestData, _ := json.Marshal(manifest)
		newDigest := digest.FromBytes(newManifestData)
		newDesc := ocispec.Descriptor{
			MediaType: desc.MediaType,
			Digest:    newDigest,
			Size:      int64(len(newManifestData)),
		}
		delete(mock.blobs, desc.Digest)
		mock.blobs[newDigest] = newManifestData
		mock.tags[tag] = newDesc
	}

	f := &HelmOCIFetcher{
		NewRepo: func(ref string) (OCIRepository, error) {
			return mock, nil
		},
	}

	result, err := f.Fetch(nopLog, source.Source{
		Name:    "test-legacy",
		Type:    "helm",
		Repo:    "oci://ghcr.io/org/charts",
		Chart:   "legacy-chart",
		Version: "v1.0.0",
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	defer os.RemoveAll(result.Dir)
}

func TestHelmOCIFetcherNoChartLayer(t *testing.T) {
	// Build a mock with no chart content layer
	configData := []byte(`{"name":"empty"}`)
	configDigest := digest.FromBytes(configData)
	manifest := ocispec.Manifest{
		MediaType: ocispec.MediaTypeImageManifest,
		Config: ocispec.Descriptor{
			MediaType: "application/vnd.cncf.helm.config.v1+json",
			Digest:    configDigest,
			Size:      int64(len(configData)),
		},
		Layers: []ocispec.Descriptor{}, // no layers
	}
	manifestData, _ := json.Marshal(manifest)
	manifestDigest := digest.FromBytes(manifestData)
	manifestDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    manifestDigest,
		Size:      int64(len(manifestData)),
	}

	mock := &mockOCIRepo{
		tags:  map[string]ocispec.Descriptor{"1.0.0": manifestDesc},
		blobs: map[digest.Digest][]byte{manifestDigest: manifestData, configDigest: configData},
	}

	f := &HelmOCIFetcher{
		NewRepo: func(ref string) (OCIRepository, error) {
			return mock, nil
		},
	}

	_, err := f.Fetch(nopLog, source.Source{
		Name:    "test-nolayer",
		Type:    "helm",
		Repo:    "oci://ghcr.io/org/charts",
		Chart:   "empty",
		Version: "v1.0.0",
	})
	if err == nil {
		t.Fatal("expected error when no chart layer exists")
	}
}
