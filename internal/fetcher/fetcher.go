// Package fetcher abstracts the retrieval of upstream CRD content.
// Each transport (Helm HTTP, Helm OCI, URL) implements the Fetcher interface.
package fetcher

import (
	"fmt"
	"strings"

	"github.com/rs/zerolog"

	"github.com/blacksd/crd-schema-extractor/internal/source"
)

// Result holds fetch output. Exactly one of Dir or Data is set.
type Result struct {
	Dir  string // temp directory (Helm charts); caller must os.RemoveAll
	Data []byte // raw bytes (URL content)
}

// Fetcher retrieves upstream content for a source.
type Fetcher interface {
	Fetch(log zerolog.Logger, src source.Source) (*Result, error)
}

// New returns the Fetcher for the given source.
func New(src source.Source) (Fetcher, error) {
	switch src.Type {
	case "helm":
		return newHelmFetcher(src), nil
	case "url":
		return &URLFetcher{}, nil
	default:
		return nil, fmt.Errorf("unknown source type: %s", src.Type)
	}
}

// newHelmFetcher returns the appropriate Helm fetcher based on the repo URL.
func newHelmFetcher(src source.Source) Fetcher {
	if strings.HasPrefix(src.Repo, "oci://") {
		return &HelmOCIFetcher{}
	}
	return &HelmHTTPFetcher{}
}
