// Command crd-schema-extractor extracts JSON schemas from Kubernetes CRDs.
package main

import (
	"fmt"
	"os"
	"time"

	"github.com/rs/zerolog"
	"github.com/spf13/cobra"
)

var (
	// Set via ldflags at build time by goreleaser.
	version = "dev"
	commit  = "unknown"
	date    = "unknown"

	debug  bool
	logger zerolog.Logger
)

func main() {
	root := &cobra.Command{
		Use:   "crd-schema-extractor <path>",
		Short: "Extract JSON schemas from Kubernetes CRDs",
		Long: `Fetches CRDs from upstream Helm charts and URLs, extracts the
openAPIV3Schema from each served version, and writes standalone
JSON Schema files with provenance metadata and a CycloneDX SBOM.

When invoked without a subcommand, runs the full extract pipeline.`,
		Args: cobra.MaximumNArgs(1),
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			logger = zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339}).
				With().Timestamp().Logger()
			if debug {
				zerolog.SetGlobalLevel(zerolog.DebugLevel)
			} else {
				zerolog.SetGlobalLevel(zerolog.InfoLevel)
			}
		},
		// Running root without a subcommand delegates to extract.
		RunE: extractRunE,
	}

	// Root-level flags that also apply when invoked without subcommand
	root.Flags().StringP("output", "o", "schemas", "Output directory for extracted schemas")
	root.Flags().Bool("fetch-only", false, "Fetch upstream content without extracting schemas")
	root.Flags().IntP("parallel", "p", 4, "Maximum number of sources to fetch in parallel")

	root.PersistentFlags().BoolVar(&debug, "debug", false, "Enable debug logging")

	root.AddCommand(newExtractCmd())
	root.AddCommand(newValidateCmd())
	root.AddCommand(newVersionCmd())

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("crd-schema-extractor %s (commit: %s, built: %s)\n", version, commit, date)
		},
	}
}
