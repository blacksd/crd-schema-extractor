// Package extractor handles fetching CRDs from upstream sources and
// extracting their openAPIV3Schema as standalone JSON Schema files.
package extractor

import (
	"encoding/json"
	"io"
	"os"
	"strings"

	"github.com/rs/zerolog"

	"github.com/blacksd/crd-schema-extractor/internal/fetcher"
	"github.com/blacksd/crd-schema-extractor/internal/source"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/util/yaml"
)

// CRDSchema represents an extracted JSON Schema for a single CRD version.
type CRDSchema struct {
	Group      string
	Kind       string
	APIVersion string
	Schema     json.RawMessage
	SourceName string // tracks which source produced this schema
}

// Extract fetches CRDs from the given source and returns extracted schemas.
func Extract(log zerolog.Logger, src source.Source) ([]CRDSchema, error) {
	f, err := fetcher.New(src)
	if err != nil {
		return nil, err
	}

	result, err := f.Fetch(log, src)
	if err != nil {
		return nil, err
	}
	// Clean up temp directory if the fetcher created one
	if result.Dir != "" {
		defer os.RemoveAll(result.Dir)
	}

	return Process(log, result, src)
}

// parseCRDs parses a multi-document YAML byte slice and extracts CRD schemas.
// It first decodes each document as a generic object to check the kind, then
// re-marshals CRD documents into the typed struct for proper schema extraction.
func parseCRDs(log zerolog.Logger, data []byte) ([]CRDSchema, error) {
	var schemas []CRDSchema

	decoder := yaml.NewYAMLOrJSONDecoder(strings.NewReader(string(data)), 4096)

	for {
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			if err == io.EOF {
				break
			}
			continue
		}

		// Quick check: is this a CRD?
		var meta struct {
			Kind string `json:"kind"`
		}
		if err := json.Unmarshal(raw, &meta); err != nil || meta.Kind != "CustomResourceDefinition" {
			continue
		}

		// Full decode into the typed CRD struct
		var crd apiextensionsv1.CustomResourceDefinition
		if err := json.Unmarshal(raw, &crd); err != nil {
			log.Warn().Err(err).Msg("decoding CRD")
			continue
		}

		if crd.Spec.Group == "" {
			continue
		}

		for _, version := range crd.Spec.Versions {
			if !version.Served {
				log.Debug().
					Str("group", crd.Spec.Group).
					Str("kind", crd.Spec.Names.Kind).
					Str("version", version.Name).
					Msg("skipping unserved version")
				continue
			}

			if version.Schema == nil || version.Schema.OpenAPIV3Schema == nil {
				log.Debug().
					Str("group", crd.Spec.Group).
					Str("kind", crd.Spec.Names.Kind).
					Str("version", version.Name).
					Msg("skipping version without openAPIV3Schema")
				continue
			}

			schemaJSON, err := json.MarshalIndent(version.Schema.OpenAPIV3Schema, "", "  ")
			if err != nil {
				log.Warn().Err(err).
					Str("group", crd.Spec.Group).
					Str("kind", crd.Spec.Names.Kind).
					Str("version", version.Name).
					Msg("marshaling schema")
				continue
			}

			log.Debug().
				Str("group", crd.Spec.Group).
				Str("kind", crd.Spec.Names.Kind).
				Str("version", version.Name).
				Int("bytes", len(schemaJSON)).
				Msg("extracted schema")

			schemas = append(schemas, CRDSchema{
				Group:      crd.Spec.Group,
				Kind:       crd.Spec.Names.Kind,
				APIVersion: version.Name,
				Schema:     schemaJSON,
			})
		}
	}

	return schemas, nil
}
