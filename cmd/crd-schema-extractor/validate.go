package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/blacksd/crd-schema-extractor/internal/source"
)

func newValidateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "validate <path>",
		Short: "Validate source configuration files",
		Long: `Parse and validate source configuration files at <path> (a directory
of YAML files or a single YAML file). Reports any structural errors
in the source definitions.

Checks that each source has required fields for its type (repo/chart
for helm, url for url), that names are unique, and that the YAML
parses correctly.`,
		Args: cobra.ExactArgs(1),
		RunE: validateRunE,
	}
}

func validateRunE(cmd *cobra.Command, args []string) error {
	log := logger
	path := args[0]

	grouped, err := source.Load(path)
	if err != nil {
		return fmt.Errorf("loading sources: %w", err)
	}

	allSources := source.All(grouped)
	if len(allSources) == 0 {
		return fmt.Errorf("no sources found in %s", path)
	}

	// Check for unique names
	names := make(map[string]bool)
	var errs []string

	for _, src := range allSources {
		if names[src.Name] {
			errs = append(errs, fmt.Sprintf("duplicate source name: %s", src.Name))
		}
		names[src.Name] = true

		// Validate required fields per type
		switch src.Type {
		case "helm":
			if src.Repo == "" {
				errs = append(errs, fmt.Sprintf("source %s: helm type requires 'repo' field", src.Name))
			}
			if src.Chart == "" {
				errs = append(errs, fmt.Sprintf("source %s: helm type requires 'chart' field", src.Name))
			}
		case "url":
			if src.URL == "" {
				errs = append(errs, fmt.Sprintf("source %s: url type requires 'url' field", src.Name))
			}
			if strings.Contains(src.URL, "{version}") && src.Version == "" {
				errs = append(errs, fmt.Sprintf("source %s: url contains {version} placeholder but version is empty", src.Name))
			}
			if src.URL != "" && !strings.Contains(src.URL, "{version}") {
				errs = append(errs, fmt.Sprintf("source %s: url must contain {version} placeholder to keep version in sync", src.Name))
			}
		case "":
			errs = append(errs, fmt.Sprintf("source %s: missing 'type' field", src.Name))
		default:
			errs = append(errs, fmt.Sprintf("source %s: unknown type %q", src.Name, src.Type))
		}

		if src.Name == "" {
			errs = append(errs, "source with empty name found")
		}
		if src.Version == "" {
			errs = append(errs, fmt.Sprintf("source %s: missing 'version' field", src.Name))
		}
		if src.License == "" {
			errs = append(errs, fmt.Sprintf("source %s: missing 'license' field", src.Name))
		}
		if src.Homepage == "" {
			errs = append(errs, fmt.Sprintf("source %s: missing 'homepage' field", src.Name))
		}
	}

	if len(errs) > 0 {
		for _, e := range errs {
			log.Error().Msg(e)
		}
		return fmt.Errorf("%d validation errors found", len(errs))
	}

	log.Info().
		Int("groups", len(grouped)).
		Int("sources", len(allSources)).
		Msg("all sources valid")
	return nil
}
