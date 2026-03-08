package fetcher

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/rs/zerolog"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/retry"

	"github.com/blacksd/crd-schema-extractor/internal/source"
)

const (
	// helmChartMediaType is the OCI media type for Helm chart content layers.
	helmChartMediaType = "application/vnd.cncf.helm.chart.content.v1.tar+gzip"
	// helmLegacyMediaType is used by older Helm versions and some registries.
	helmLegacyMediaType = "application/tar+gzip"
)

// HelmOCIFetcher fetches Helm charts from OCI registries using oras-go.
type HelmOCIFetcher struct {
	// NewRepo allows injecting a custom repository constructor for testing.
	// If nil, remote.NewRepository is used.
	NewRepo func(ref string) (OCIRepository, error)
}

// OCIRepository abstracts the OCI registry operations needed for chart pulling.
type OCIRepository interface {
	Resolve(ctx context.Context, reference string) (ocispec.Descriptor, error)
	Fetch(ctx context.Context, target ocispec.Descriptor) (io.ReadCloser, error)
}

func (f *HelmOCIFetcher) Fetch(log zerolog.Logger, src source.Source) (*Result, error) {
	ctx := context.Background()

	// Build OCI reference: strip oci:// prefix, append chart name
	ref := strings.TrimPrefix(src.Repo, "oci://")
	ref = strings.TrimSuffix(ref, "/") + "/" + src.Chart

	log.Debug().Str("ref", ref).Msg("pulling chart from OCI registry")

	repo, err := f.newRepo(ref)
	if err != nil {
		return nil, fmt.Errorf("creating OCI repository for %s: %w", ref, err)
	}

	// Resolve the tag with version fallback (try without v prefix first)
	tag := strings.TrimPrefix(src.Version, "v")
	manifestDesc, err := repo.Resolve(ctx, tag)
	if err != nil {
		log.Debug().Str("version", tag).Err(err).Msg("version not found, retrying with original")
		tag = src.Version
		manifestDesc, err = repo.Resolve(ctx, tag)
		if err != nil {
			return nil, fmt.Errorf("resolving %s:%s: %w", ref, src.Version, err)
		}
	}

	// Fetch and parse the OCI manifest
	manifestRC, err := repo.Fetch(ctx, manifestDesc)
	if err != nil {
		return nil, fmt.Errorf("fetching manifest for %s:%s: %w", ref, tag, err)
	}
	manifestBytes, err := content.ReadAll(manifestRC, manifestDesc)
	manifestRC.Close()
	if err != nil {
		return nil, fmt.Errorf("reading manifest for %s:%s: %w", ref, tag, err)
	}

	var manifest ocispec.Manifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return nil, fmt.Errorf("parsing OCI manifest for %s:%s: %w", ref, tag, err)
	}

	// Find the chart content layer
	layerDesc, err := findChartLayer(manifest.Layers)
	if err != nil {
		return nil, fmt.Errorf("no chart layer in %s:%s: %w", ref, tag, err)
	}

	// Fetch the chart tarball
	log.Debug().Str("digest", string(layerDesc.Digest)).Int64("size", layerDesc.Size).Msg("fetching chart layer")
	layerRC, err := repo.Fetch(ctx, layerDesc)
	if err != nil {
		return nil, fmt.Errorf("fetching chart layer from %s:%s: %w", ref, tag, err)
	}
	chartData, err := content.ReadAll(layerRC, layerDesc)
	layerRC.Close()
	if err != nil {
		return nil, fmt.Errorf("reading chart layer from %s:%s: %w", ref, tag, err)
	}

	// Extract to temp directory
	tmpDir, err := os.MkdirTemp("", "crd-schemas-oci-*")
	if err != nil {
		return nil, fmt.Errorf("creating temp dir: %w", err)
	}

	chartDir := filepath.Join(tmpDir, "chart")
	if err := os.MkdirAll(chartDir, 0755); err != nil {
		os.RemoveAll(tmpDir)
		return nil, fmt.Errorf("creating chart dir: %w", err)
	}

	if err := extractTarGz(chartData, chartDir); err != nil {
		os.RemoveAll(tmpDir)
		return nil, fmt.Errorf("extracting chart %s:%s: %w", ref, tag, err)
	}

	log.Debug().Str("dir", chartDir).Msg("OCI chart extracted")
	return &Result{Dir: tmpDir}, nil
}

func (f *HelmOCIFetcher) newRepo(ref string) (OCIRepository, error) {
	if f.NewRepo != nil {
		return f.NewRepo(ref)
	}
	repo, err := remote.NewRepository(ref)
	if err != nil {
		return nil, err
	}
	// Set up anonymous auth client that handles token challenges.
	// This works for public registries (ghcr.io, docker.io, etc.).
	// For private registries, Docker credential helpers would be needed here.
	repo.Client = &auth.Client{
		Client: retry.DefaultClient,
		Cache:  auth.NewCache(),
	}
	return repo, nil
}

// findChartLayer locates the Helm chart content layer in an OCI manifest.
func findChartLayer(layers []ocispec.Descriptor) (ocispec.Descriptor, error) {
	for _, l := range layers {
		if l.MediaType == helmChartMediaType || l.MediaType == helmLegacyMediaType {
			return l, nil
		}
	}
	return ocispec.Descriptor{}, fmt.Errorf("no layer with media type %s or %s", helmChartMediaType, helmLegacyMediaType)
}
