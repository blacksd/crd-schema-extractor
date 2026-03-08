package fetcher

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rs/zerolog"

	"github.com/blacksd/crd-schema-extractor/internal/source"
)

// HelmFetcher fetches Helm charts from HTTP or OCI repositories.
type HelmFetcher struct {
	Runner CommandRunner
}

func (f *HelmFetcher) Fetch(log zerolog.Logger, src source.Source) (*Result, error) {
	tmpDir, err := os.MkdirTemp("", "crd-schemas-helm-*")
	if err != nil {
		return nil, fmt.Errorf("creating temp dir: %w", err)
	}

	chartDir := filepath.Join(tmpDir, "chart")
	isOCI := strings.HasPrefix(src.Repo, "oci://")

	if isOCI {
		if err := f.pullOCI(log, src, chartDir); err != nil {
			os.RemoveAll(tmpDir)
			return nil, err
		}
	} else {
		if err := f.pullHTTP(log, src, chartDir); err != nil {
			os.RemoveAll(tmpDir)
			return nil, err
		}
	}

	return &Result{Dir: tmpDir}, nil
}

func (f *HelmFetcher) pullOCI(log zerolog.Logger, src source.Source, chartDir string) error {
	chartRef := strings.TrimSuffix(src.Repo, "/") + "/" + src.Chart
	log.Debug().Str("chart", chartRef).Msg("pulling chart from OCI registry")

	helmVersion := strings.TrimPrefix(src.Version, "v")
	err := f.Runner.Run("helm", "pull", chartRef, "--version", helmVersion, "--untar", "--untardir", chartDir)
	if err != nil {
		log.Debug().Str("version", src.Version).Msg("retrying pull with original version string")
		err = f.Runner.Run("helm", "pull", chartRef, "--version", src.Version, "--untar", "--untardir", chartDir)
		if err != nil {
			return fmt.Errorf("helm pull %s@%s: %w", chartRef, src.Version, err)
		}
	}
	return nil
}

func (f *HelmFetcher) pullHTTP(log zerolog.Logger, src source.Source, chartDir string) error {
	repoAlias := "crd-schemas-" + src.Name

	log.Debug().Str("repo", src.Repo).Str("alias", repoAlias).Msg("adding helm repo")
	if err := f.Runner.Run("helm", "repo", "add", repoAlias, src.Repo, "--force-update"); err != nil {
		return fmt.Errorf("helm repo add: %w", err)
	}

	log.Debug().Str("alias", repoAlias).Msg("updating helm repo")
	if err := f.Runner.Run("helm", "repo", "update", repoAlias); err != nil {
		return fmt.Errorf("helm repo update: %w", err)
	}

	helmVersion := strings.TrimPrefix(src.Version, "v")
	chartRef := repoAlias + "/" + src.Chart

	log.Debug().Str("chart", chartRef).Str("version", helmVersion).Msg("pulling chart")
	err := f.Runner.Run("helm", "pull", chartRef, "--version", helmVersion, "--untar", "--untardir", chartDir)
	if err != nil {
		log.Debug().Str("version", src.Version).Msg("retrying pull with original version string")
		err = f.Runner.Run("helm", "pull", chartRef, "--version", src.Version, "--untar", "--untardir", chartDir)
		if err != nil {
			return fmt.Errorf("helm pull %s@%s: %w", src.Chart, src.Version, err)
		}
	}
	return nil
}
