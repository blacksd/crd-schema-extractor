// Package fetcher abstracts the retrieval of upstream CRD content.
// Each transport (Helm, URL, etc.) implements the Fetcher interface.
package fetcher

import (
	"fmt"
	"os"
	"os/exec"

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

// CommandRunner abstracts exec.Command for testability.
type CommandRunner interface {
	Run(name string, args ...string) error
	Output(name string, args ...string) ([]byte, error)
}

// ExecRunner is the production CommandRunner using os/exec.
type ExecRunner struct{}

func (r *ExecRunner) Run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (r *ExecRunner) Output(name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	return cmd.Output()
}

// New returns the Fetcher for the given source type.
func New(sourceType string, runner CommandRunner) (Fetcher, error) {
	switch sourceType {
	case "helm":
		return &HelmFetcher{Runner: runner}, nil
	case "url":
		return &URLFetcher{}, nil
	default:
		return nil, fmt.Errorf("unknown source type: %s", sourceType)
	}
}
