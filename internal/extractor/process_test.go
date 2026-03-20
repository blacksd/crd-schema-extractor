package extractor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/blacksd/crd-schema-extractor/internal/fetcher"
	"github.com/blacksd/crd-schema-extractor/internal/source"
)

const rawCRDYAML = `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: foos.example.com
spec:
  group: example.com
  names:
    kind: Foo
    plural: foos
    singular: foo
  scope: Namespaced
  versions:
    - name: v1
      served: true
      storage: true
      schema:
        openAPIV3Schema:
          type: object
          properties:
            spec:
              type: object
`

const templatedCRDYAML = `{{- if .Values.crds.install }}
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: bars.example.com
  labels:
    {{- include "chart.labels" . | nindent 4 }}
spec:
  group: example.com
  names:
    kind: Bar
    plural: bars
    singular: bar
  scope: Namespaced
  versions:
    - name: v1
      served: true
      storage: true
      schema:
        openAPIV3Schema:
          type: object
          properties:
            spec:
              type: object
{{- end }}
`

// setupChartDir creates a minimal chart directory structure for testing.
// Returns the tmpDir (which should be cleaned up by the caller).
func setupChartDir(t *testing.T, chartName string, files map[string]string) string {
	t.Helper()
	tmpDir := t.TempDir()
	chartRoot := filepath.Join(tmpDir, "chart", chartName)

	for relPath, content := range files {
		fullPath := filepath.Join(chartRoot, relPath)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
			t.Fatalf("creating dir for %s: %v", relPath, err)
		}
		if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
			t.Fatalf("writing %s: %v", relPath, err)
		}
	}

	return tmpDir
}

func TestProcessHelmCrdsDir(t *testing.T) {
	dir := setupChartDir(t, "test-chart", map[string]string{
		"crds/foo-crd.yaml": rawCRDYAML,
	})

	result := &fetcher.Result{Dir: dir}
	src := source.Source{Name: "test", Type: "helm", Chart: "test-chart"}

	schemas, err := Process(nopLog, result, src)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(schemas) != 1 {
		t.Fatalf("expected 1 schema, got %d", len(schemas))
	}
	if schemas[0].Kind != "Foo" {
		t.Errorf("kind = %q, want Foo", schemas[0].Kind)
	}
}

func TestProcessHelmTemplatesDir(t *testing.T) {
	dir := setupChartDir(t, "test-chart", map[string]string{
		"templates/bar-crd.yaml": templatedCRDYAML,
	})

	result := &fetcher.Result{Dir: dir}
	src := source.Source{Name: "test", Type: "helm", Chart: "test-chart"}

	schemas, err := Process(nopLog, result, src)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(schemas) != 1 {
		t.Fatalf("expected 1 schema, got %d", len(schemas))
	}
	if schemas[0].Kind != "Bar" {
		t.Errorf("kind = %q, want Bar", schemas[0].Kind)
	}
}

func TestProcessHelmBothDirsDedups(t *testing.T) {
	// Same CRD in both crds/ and templates/ should be deduped
	dir := setupChartDir(t, "test-chart", map[string]string{
		"crds/foo-crd.yaml":      rawCRDYAML,
		"templates/foo-crd.yaml": rawCRDYAML, // same CRD, no templating
	})

	result := &fetcher.Result{Dir: dir}
	src := source.Source{Name: "test", Type: "helm", Chart: "test-chart"}

	schemas, err := Process(nopLog, result, src)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(schemas) != 1 {
		t.Fatalf("expected 1 schema after dedup, got %d", len(schemas))
	}
}

func TestProcessHelmNonStandardDir(t *testing.T) {
	// Kargo-style: CRDs in resources/crds/
	dir := setupChartDir(t, "test-chart", map[string]string{
		"resources/crds/foo-crd.yaml": rawCRDYAML,
	})

	result := &fetcher.Result{Dir: dir}
	src := source.Source{Name: "test", Type: "helm", Chart: "test-chart"}

	schemas, err := Process(nopLog, result, src)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(schemas) != 1 {
		t.Fatalf("expected 1 schema from non-standard dir, got %d", len(schemas))
	}
	if schemas[0].Kind != "Foo" {
		t.Errorf("kind = %q, want Foo", schemas[0].Kind)
	}
}

func TestProcessHelmNoCRDs(t *testing.T) {
	dir := setupChartDir(t, "test-chart", map[string]string{
		"templates/deployment.yaml": `apiVersion: apps/v1
kind: Deployment
metadata:
  name: my-app
`,
	})

	result := &fetcher.Result{Dir: dir}
	src := source.Source{Name: "test", Type: "helm", Chart: "test-chart"}

	schemas, err := Process(nopLog, result, src)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(schemas) != 0 {
		t.Errorf("expected 0 schemas, got %d", len(schemas))
	}
}

func TestProcessRaw(t *testing.T) {
	result := &fetcher.Result{Data: []byte(rawCRDYAML)}
	src := source.Source{Name: "test", Type: "url"}

	schemas, err := Process(nopLog, result, src)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(schemas) != 1 {
		t.Fatalf("expected 1 schema, got %d", len(schemas))
	}
	if schemas[0].Kind != "Foo" {
		t.Errorf("kind = %q, want Foo", schemas[0].Kind)
	}
}

func TestProcessUnknownType(t *testing.T) {
	result := &fetcher.Result{Data: []byte("data")}
	src := source.Source{Name: "test", Type: "foobar"}

	_, err := Process(nopLog, result, src)
	if err == nil {
		t.Fatal("expected error for unknown source type")
	}
}

func TestProcessHelmMultipleCRDsInOneFile(t *testing.T) {
	multiCRD := rawCRDYAML + "---\n" + `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: bars.example.com
spec:
  group: example.com
  names:
    kind: Bar
    plural: bars
    singular: bar
  scope: Namespaced
  versions:
    - name: v1
      served: true
      storage: true
      schema:
        openAPIV3Schema:
          type: object
`
	dir := setupChartDir(t, "test-chart", map[string]string{
		"crds/combined.yaml": multiCRD,
	})

	result := &fetcher.Result{Dir: dir}
	src := source.Source{Name: "test", Type: "helm", Chart: "test-chart"}

	schemas, err := Process(nopLog, result, src)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(schemas) != 2 {
		t.Fatalf("expected 2 schemas from multi-doc file, got %d", len(schemas))
	}
}

// setupGitRepoDir creates a directory structure mimicking an extracted git repo archive.
func setupGitRepoDir(t *testing.T, files map[string]string) string {
	t.Helper()
	tmpDir := t.TempDir()
	repoRoot := filepath.Join(tmpDir, "repo")

	for relPath, content := range files {
		fullPath := filepath.Join(repoRoot, relPath)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
			t.Fatalf("creating dir for %s: %v", relPath, err)
		}
		if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
			t.Fatalf("writing %s: %v", relPath, err)
		}
	}

	return tmpDir
}

func TestProcessGit(t *testing.T) {
	dir := setupGitRepoDir(t, map[string]string{
		"cluster/crds/foo-crd.yaml": rawCRDYAML,
	})

	result := &fetcher.Result{Dir: dir}
	src := source.Source{Name: "test", Type: "git+github"}

	schemas, err := Process(nopLog, result, src)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(schemas) != 1 {
		t.Fatalf("expected 1 schema, got %d", len(schemas))
	}
	if schemas[0].Kind != "Foo" {
		t.Errorf("kind = %q, want Foo", schemas[0].Kind)
	}
}

func TestProcessGitWithPath(t *testing.T) {
	dir := setupGitRepoDir(t, map[string]string{
		"cluster/crds/foo-crd.yaml":                    rawCRDYAML,
		"pkg/k8s/apis/cilium.io/client/crds/bar.yaml":  rawCRDYAML,
	})

	result := &fetcher.Result{Dir: dir}
	src := source.Source{Name: "test", Type: "git+github", Path: "cluster/crds"}

	schemas, err := Process(nopLog, result, src)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(schemas) != 1 {
		t.Fatalf("expected 1 schema (path-restricted), got %d", len(schemas))
	}
}

func TestProcessGitInvalidPath(t *testing.T) {
	dir := setupGitRepoDir(t, map[string]string{
		"crds/foo.yaml": rawCRDYAML,
	})

	result := &fetcher.Result{Dir: dir}
	src := source.Source{Name: "test", Type: "git+github", Path: "nonexistent/path"}

	_, err := Process(nopLog, result, src)
	if err == nil {
		t.Fatal("expected error for nonexistent path")
	}
}

func TestProcessGitNoTemplateStripping(t *testing.T) {
	// A file in a templates/ directory should NOT have template directives stripped
	// when processed as a git source. The raw {{ }} content means it won't parse
	// as valid CRD YAML, so we should get 0 schemas (not 1 like helm would).
	dir := setupGitRepoDir(t, map[string]string{
		"templates/foo-crd.yaml": templatedCRDYAML,
	})

	result := &fetcher.Result{Dir: dir}
	src := source.Source{Name: "test", Type: "git+github"}

	schemas, err := Process(nopLog, result, src)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	// Template directives are not stripped, so the YAML is unparseable as a CRD
	if len(schemas) != 0 {
		t.Errorf("expected 0 schemas (no template stripping), got %d", len(schemas))
	}
}

func TestProcessHelmTemplatedInSubdir(t *testing.T) {
	// CRD in a subdirectory of templates/
	dir := setupChartDir(t, "test-chart", map[string]string{
		"templates/crds/foo-crd.yaml": templatedCRDYAML,
	})

	result := &fetcher.Result{Dir: dir}
	src := source.Source{Name: "test", Type: "helm", Chart: "test-chart"}

	schemas, err := Process(nopLog, result, src)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	// The scanDir for templates/ is non-recursive, but scanTree should pick this up
	if len(schemas) != 1 {
		t.Fatalf("expected 1 schema from templates subdir, got %d", len(schemas))
	}
}
