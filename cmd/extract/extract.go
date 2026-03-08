package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/blacksd/crd-schema-extractor/internal/extractor"
	"github.com/blacksd/crd-schema-extractor/internal/fetcher"
	"github.com/blacksd/crd-schema-extractor/internal/provenance"
	"github.com/blacksd/crd-schema-extractor/internal/sbom"
	"github.com/blacksd/crd-schema-extractor/internal/source"
)

func newExtractCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "extract <path>",
		Short: "Fetch CRDs and extract JSON schemas",
		Long: `Fetch CRDs from all sources defined in <path> (a directory of YAML
files or a single YAML file), extract the openAPIV3Schema from each
served version, and write standalone JSON Schema files.

With --fetch-only, fetches upstream content and reports what was
retrieved without extracting or writing schemas.`,
		Args: cobra.MaximumNArgs(1),
		RunE: extractRunE,
	}

	cmd.Flags().StringP("output", "o", "schemas", "Output directory for extracted schemas")
	cmd.Flags().Bool("fetch-only", false, "Fetch upstream content without extracting schemas")

	return cmd
}

func extractRunE(cmd *cobra.Command, args []string) error {
	sourcesPath := "sources"
	if len(args) > 0 {
		sourcesPath = args[0]
	}

	outputDir, _ := cmd.Flags().GetString("output")
	fetchOnly, _ := cmd.Flags().GetBool("fetch-only")

	if fetchOnly {
		return runFetchOnly(sourcesPath, outputDir)
	}
	return runExtract(sourcesPath, outputDir)
}

// schemaEntry tracks an extracted schema alongside its source metadata.
type schemaEntry struct {
	schema extractor.CRDSchema
	src    source.Source
}

func runExtract(sourcesPath, outputDir string) error {
	log := logger
	timestamp := time.Now().UTC().Format(time.RFC3339)

	grouped, err := source.Load(sourcesPath)
	if err != nil {
		return err
	}

	log.Debug().Str("path", sourcesPath).Msg("loaded source configs")

	// Phase 1: Extract and filter all schemas
	var allEntries []schemaEntry
	var allSchemas []extractor.CRDSchema
	totalSources := 0

	for _, sources := range grouped {
		for _, src := range sources {
			totalSources++
			srcLog := log.With().Str("source", src.Name).Str("type", src.Type).Str("version", src.Version).Logger()
			srcLog.Info().Msg("processing")

			schemas, err := extractor.Extract(srcLog, src)
			if err != nil {
				srcLog.Warn().Err(err).Msg("extraction failed")
				continue
			}

			// Tag schemas with their source name
			for i := range schemas {
				schemas[i].SourceName = src.Name
			}

			// Apply include/exclude filters
			schemas = extractor.Filter(schemas, src)

			srcLog.Info().Int("count", len(schemas)).Msg("schemas extracted")

			for _, s := range schemas {
				allEntries = append(allEntries, schemaEntry{schema: s, src: src})
				allSchemas = append(allSchemas, s)
			}
		}
	}

	// Phase 2: Detect conflicts
	conflicts := extractor.DetectConflicts(log, allSchemas)
	if len(conflicts) > 0 {
		for key, entries := range conflicts {
			sources := make([]string, len(entries))
			for i, e := range entries {
				sources[i] = e.SourceName
			}
			log.Error().
				Str("schema", key).
				Strs("sources", sources).
				Msg("schema conflict")
		}
		return fmt.Errorf("%d schema conflicts detected, resolve with include/exclude filters", len(conflicts))
	}

	// Phase 3: Deduplicate identical schemas (keep first occurrence)
	seen := make(map[string]bool)
	var dedupedEntries []schemaEntry
	for _, e := range allEntries {
		key := extractor.SchemaKey(e.schema)
		if seen[key] {
			continue
		}
		seen[key] = true
		dedupedEntries = append(dedupedEntries, e)
	}

	// Phase 4: Write schemas to disk
	written, skipped := 0, 0
	for _, e := range dedupedEntries {
		schema := e.schema
		src := e.src

		kindLower := strings.ToLower(schema.Kind)
		schemaFilename := kindLower + ".json"
		provFilename := kindLower + ".provenance.json"
		group := schema.Group

		schemaDir := filepath.Join(outputDir, group, schema.APIVersion)
		schemaPath := filepath.Join(schemaDir, schemaFilename)
		changed := !fileContentEqual(schemaPath, schema.Schema)

		if changed {
			provJSON, err := provenance.Generate(
				schemaFilename, group, schema.Kind, schema.APIVersion,
				timestamp, src, schema.Schema,
			)
			if err != nil {
				log.Warn().Err(err).Str("group", group).Str("kind", schema.Kind).Msg("generating provenance")
				continue
			}

			if err := os.MkdirAll(schemaDir, 0755); err != nil {
				return err
			}
			if err := os.WriteFile(schemaPath, schema.Schema, 0644); err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(schemaDir, provFilename), provJSON, 0644); err != nil {
				return err
			}

			log.Debug().Str("group", group).Str("kind", schema.Kind).Str("apiVersion", schema.APIVersion).Msg("written schema")
			written++
		} else {
			log.Debug().Str("group", group).Str("kind", schema.Kind).Str("apiVersion", schema.APIVersion).Msg("unchanged, skipping")
			skipped++
		}
	}

	// Generate SBOM
	allSources := source.All(grouped)
	sbomJSON, err := sbom.Generate(allSources, timestamp)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return err
	}
	sbomPath := filepath.Join(outputDir, "sbom.cdx.json")
	if err := os.WriteFile(sbomPath, sbomJSON, 0644); err != nil {
		return err
	}

	log.Info().
		Int("sources", totalSources).
		Int("schemas", len(dedupedEntries)).
		Int("written", written).
		Int("unchanged", skipped).
		Msg("extraction complete")
	return nil
}

func runFetchOnly(sourcesPath, outputDir string) error {
	log := logger

	grouped, err := source.Load(sourcesPath)
	if err != nil {
		return err
	}

	totalSources := 0
	for _, sources := range grouped {
		for _, src := range sources {
			totalSources++
			srcLog := log.With().Str("source", src.Name).Str("type", src.Type).Str("version", src.Version).Logger()
			srcLog.Info().Msg("fetching")

			f, err := fetcher.New(src)
			if err != nil {
				srcLog.Warn().Err(err).Msg("creating fetcher failed")
				continue
			}

			result, err := f.Fetch(srcLog, src)
			if err != nil {
				srcLog.Warn().Err(err).Msg("fetch failed")
				continue
			}

			destDir := filepath.Join(outputDir, src.Name)
			if err := os.MkdirAll(destDir, 0755); err != nil {
				return fmt.Errorf("creating output dir for %s: %w", src.Name, err)
			}

			if result.Dir != "" {
				// Move chart contents from temp dir into output
				chartDir := filepath.Join(result.Dir, "chart")
				entries, err := os.ReadDir(chartDir)
				if err != nil {
					srcLog.Warn().Err(err).Msg("reading fetched chart dir")
					os.RemoveAll(result.Dir)
					continue
				}
				for _, entry := range entries {
					from := filepath.Join(chartDir, entry.Name())
					to := filepath.Join(destDir, entry.Name())
					if err := os.Rename(from, to); err != nil {
						srcLog.Warn().Err(err).Str("from", from).Str("to", to).Msg("moving chart content")
					}
				}
				os.RemoveAll(result.Dir)
				srcLog.Info().Str("dir", destDir).Msg("fetched chart")
			} else {
				// Write raw manifest data to file
				manifestPath := filepath.Join(destDir, "manifest.yaml")
				if err := os.WriteFile(manifestPath, result.Data, 0644); err != nil {
					return fmt.Errorf("writing manifest for %s: %w", src.Name, err)
				}
				srcLog.Info().Str("file", manifestPath).Int("bytes", len(result.Data)).Msg("fetched manifest")
			}
		}
	}

	log.Info().Int("sources", totalSources).Str("output", outputDir).Msg("fetch complete")
	return nil
}

// fileContentEqual returns true if the file at path exists and its content
// has the same SHA-256 hash as data.
func fileContentEqual(path string, data []byte) bool {
	existing, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return sha256.Sum256(existing) == sha256.Sum256(data)
}
