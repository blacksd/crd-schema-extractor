package extractor

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rs/zerolog"

	"github.com/blacksd/crd-schema-extractor/internal/fetcher"
	"github.com/blacksd/crd-schema-extractor/internal/source"
)

// crdMarker is used as a cheap pre-filter before attempting full YAML parsing.
var crdMarker = []byte("kind: CustomResourceDefinition")

// Process converts a fetcher result into extracted CRD schemas.
// The strategy is determined by source type.
func Process(log zerolog.Logger, result *fetcher.Result, src source.Source) ([]CRDSchema, error) {
	switch src.Type {
	case "helm":
		return processHelm(log, result, src)
	case "url":
		return processRaw(log, result.Data)
	default:
		return nil, fmt.Errorf("unknown source type for processing: %s", src.Type)
	}
}

// processRaw parses raw YAML bytes (from URL fetch) into CRD schemas.
func processRaw(log zerolog.Logger, data []byte) ([]CRDSchema, error) {
	return parseCRDs(log, data)
}

// processHelm scans an unpacked Helm chart directory for CRD files.
// It scans the crds/ directory (raw YAML) and the entire chart tree for
// YAML files containing CRD markers, applying template directive stripping
// to files found in templates/ directories.
func processHelm(log zerolog.Logger, result *fetcher.Result, src source.Source) ([]CRDSchema, error) {
	chartRoot := filepath.Join(result.Dir, "chart", src.Chart)

	var allSchemas []CRDSchema
	scanned := make(map[string]bool) // track scanned files by absolute path

	// 1. Scan crds/ directory (standard Helm CRD location, raw YAML)
	crdsDir := filepath.Join(chartRoot, "crds")
	schemas, err := scanDir(log, crdsDir, false, scanned)
	if err != nil {
		log.Debug().Err(err).Str("dir", crdsDir).Msg("no crds directory found, skipping")
	} else {
		allSchemas = append(allSchemas, schemas...)
	}

	// 2. Scan templates/ directory (apply template stripping)
	templatesDir := filepath.Join(chartRoot, "templates")
	schemas, err = scanDir(log, templatesDir, true, scanned)
	if err != nil {
		log.Debug().Err(err).Str("dir", templatesDir).Msg("no templates directory found, skipping")
	} else {
		allSchemas = append(allSchemas, schemas...)
	}

	// 3. Scan remaining directories recursively for CRD files.
	// This catches non-standard layouts like Kargo's resources/crds/.
	schemas, err = scanTree(log, chartRoot, scanned)
	if err != nil {
		log.Warn().Err(err).Msg("scanning chart tree")
	} else {
		allSchemas = append(allSchemas, schemas...)
	}

	return dedup(allSchemas), nil
}

// scanDir reads all YAML files in a single directory (non-recursive).
// If strip is true, template directives are removed before parsing.
func scanDir(log zerolog.Logger, dir string, strip bool, scanned map[string]bool) ([]CRDSchema, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var schemas []CRDSchema
	log.Debug().Str("dir", dir).Int("files", len(entries)).Bool("strip", strip).Msg("scanning directory")

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !isYAMLFile(name) {
			continue
		}

		path := filepath.Join(dir, name)
		absPath, _ := filepath.Abs(path)
		if scanned[absPath] {
			continue
		}
		scanned[absPath] = true

		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		if strip {
			data = StripTemplateDirectives(data)
		}

		if !bytes.Contains(data, crdMarker) {
			continue
		}

		parsed, err := parseCRDs(log, data)
		if err != nil {
			log.Warn().Err(err).Str("file", name).Msg("parsing CRDs failed")
			continue
		}
		schemas = append(schemas, parsed...)
	}

	return schemas, nil
}

// scanTree walks the chart directory recursively, scanning any YAML files
// not already processed by scanDir. Files under templates/ subtrees get
// template stripping applied.
func scanTree(log zerolog.Logger, root string, scanned map[string]bool) ([]CRDSchema, error) {
	var schemas []CRDSchema

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip inaccessible entries
		}
		if d.IsDir() {
			return nil
		}
		if !isYAMLFile(d.Name()) {
			return nil
		}

		absPath, _ := filepath.Abs(path)
		if scanned[absPath] {
			return nil
		}
		scanned[absPath] = true

		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		// Apply template stripping if the file is under a templates/ directory
		if inTemplatesDir(path) {
			data = StripTemplateDirectives(data)
		}

		if !bytes.Contains(data, crdMarker) {
			return nil
		}

		parsed, parseErr := parseCRDs(log, data)
		if parseErr != nil {
			log.Warn().Err(parseErr).Str("file", path).Msg("parsing CRDs failed")
			return nil
		}
		if len(parsed) > 0 {
			log.Debug().Str("file", path).Int("count", len(parsed)).Msg("found CRDs in non-standard location")
		}
		schemas = append(schemas, parsed...)
		return nil
	})

	return schemas, err
}

func isYAMLFile(name string) bool {
	return strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml")
}

// inTemplatesDir checks if a file path contains a "templates" directory component.
func inTemplatesDir(path string) bool {
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if part == "templates" {
			return true
		}
	}
	return false
}
