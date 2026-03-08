package fetcher

import (
	"fmt"
	"io"
	"os"
	"testing"

	"github.com/rs/zerolog"

	"github.com/blacksd/crd-schema-extractor/internal/source"
)

var nopLog = zerolog.New(io.Discard)

func TestHelmFetcherOCI(t *testing.T) {
	mock := &MockRunner{}
	f := &HelmFetcher{Runner: mock}

	src := source.Source{
		Name:    "test-oci",
		Type:    "helm",
		Repo:    "oci://ghcr.io/org/charts",
		Chart:   "my-chart",
		Version: "v1.2.3",
	}

	result, err := f.Fetch(nopLog, src)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	defer os.RemoveAll(result.Dir)

	if result.Dir == "" {
		t.Fatal("expected Dir to be set")
	}
	if result.Data != nil {
		t.Error("expected Data to be nil for helm fetch")
	}

	// OCI should NOT call repo add or repo update
	if mock.HasCallWithArgs("repo", "add", "crd-schemas-test-oci", "oci://ghcr.io/org/charts", "--force-update") {
		t.Error("OCI fetch should not call repo add")
	}
	if mock.HasCallWithArgs("repo", "update", "crd-schemas-test-oci") {
		t.Error("OCI fetch should not call repo update")
	}

	// Should call pull with the full OCI URI
	if !mock.HasCallWithArgs("pull", "oci://ghcr.io/org/charts/my-chart", "--version", "1.2.3", "--untar", "--untardir", result.Dir+"/chart") {
		t.Errorf("expected OCI pull call, got: %v", mock.Calls)
	}
}

func TestHelmFetcherHTTP(t *testing.T) {
	mock := &MockRunner{}
	f := &HelmFetcher{Runner: mock}

	src := source.Source{
		Name:    "test-http",
		Type:    "helm",
		Repo:    "https://charts.example.io",
		Chart:   "my-chart",
		Version: "v2.0.0",
	}

	result, err := f.Fetch(nopLog, src)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	defer os.RemoveAll(result.Dir)

	// Should call: repo add, repo update, pull (in that order)
	if mock.CallCount() != 3 {
		t.Fatalf("expected 3 calls (add, update, pull), got %d: %v", mock.CallCount(), mock.Calls)
	}

	// Call 0: repo add
	args := mock.CallArgs(0)
	if len(args) < 2 || args[0] != "repo" || args[1] != "add" {
		t.Errorf("call 0 should be repo add, got: %v", args)
	}

	// Call 1: repo update
	args = mock.CallArgs(1)
	if len(args) < 2 || args[0] != "repo" || args[1] != "update" {
		t.Errorf("call 1 should be repo update, got: %v", args)
	}

	// Call 2: pull
	args = mock.CallArgs(2)
	if len(args) < 1 || args[0] != "pull" {
		t.Errorf("call 2 should be pull, got: %v", args)
	}
}

func TestHelmFetcherVersionFallback(t *testing.T) {
	callCount := 0
	mock := &MockRunner{
		RunFunc: func(name string, args ...string) error {
			callCount++
			// Fail on the first pull (with stripped v prefix), succeed on retry
			for _, arg := range args {
				if arg == "pull" {
					if callCount == 1 {
						return fmt.Errorf("version not found")
					}
					return nil
				}
			}
			return nil
		},
	}
	f := &HelmFetcher{Runner: mock}

	src := source.Source{
		Name:    "test-fallback",
		Type:    "helm",
		Repo:    "oci://ghcr.io/org/charts",
		Chart:   "my-chart",
		Version: "v1.0.0",
	}

	result, err := f.Fetch(nopLog, src)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	defer os.RemoveAll(result.Dir)

	// Should have made 2 calls: first pull fails, second succeeds
	if mock.CallCount() != 2 {
		t.Errorf("expected 2 calls (failed pull + retry), got %d", mock.CallCount())
	}
}

func TestHelmFetcherPullFailure(t *testing.T) {
	mock := &MockRunner{
		RunErr: fmt.Errorf("pull failed"),
	}
	f := &HelmFetcher{Runner: mock}

	src := source.Source{
		Name:    "test-fail",
		Type:    "helm",
		Repo:    "oci://ghcr.io/org/charts",
		Chart:   "my-chart",
		Version: "v1.0.0",
	}

	_, err := f.Fetch(nopLog, src)
	if err == nil {
		t.Fatal("expected error on pull failure")
	}
}
