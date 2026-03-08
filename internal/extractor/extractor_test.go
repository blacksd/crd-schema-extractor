package extractor

import (
	"encoding/json"
	"io"
	"testing"

	"github.com/rs/zerolog"
)

// nopLog returns a disabled logger for tests that don't care about log output.
var nopLog = zerolog.New(io.Discard)

// Minimal CRD YAML for testing the parser.
const testCRDYAML = `apiVersion: apiextensions.k8s.io/v1
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
              properties:
                replicas:
                  type: integer
    - name: v1beta1
      served: true
      storage: false
      schema:
        openAPIV3Schema:
          type: object
          properties:
            spec:
              type: object
    - name: v1alpha1
      served: false
      storage: false
      schema:
        openAPIV3Schema:
          type: object
`

func TestParseCRDs(t *testing.T) {
	schemas, err := parseCRDs(nopLog, []byte(testCRDYAML))
	if err != nil {
		t.Fatalf("parseCRDs: %v", err)
	}

	// Should extract v1 and v1beta1 (both served), skip v1alpha1 (not served)
	if len(schemas) != 2 {
		t.Fatalf("expected 2 schemas, got %d", len(schemas))
	}

	byVersion := map[string]CRDSchema{}
	for _, s := range schemas {
		byVersion[s.APIVersion] = s
	}

	v1, ok := byVersion["v1"]
	if !ok {
		t.Fatal("expected v1 schema not found")
	}
	if v1.Group != "example.com" {
		t.Errorf("v1 group = %q, want %q", v1.Group, "example.com")
	}
	if v1.Kind != "Foo" {
		t.Errorf("v1 kind = %q, want %q", v1.Kind, "Foo")
	}

	// Verify the schema is valid JSON
	var parsed map[string]interface{}
	if err := json.Unmarshal(v1.Schema, &parsed); err != nil {
		t.Errorf("v1 schema is not valid JSON: %v", err)
	}
	if parsed["type"] != "object" {
		t.Errorf("v1 schema type = %v, want %q", parsed["type"], "object")
	}

	if _, ok := byVersion["v1beta1"]; !ok {
		t.Error("expected v1beta1 schema not found")
	}

	if _, ok := byVersion["v1alpha1"]; ok {
		t.Error("v1alpha1 should have been skipped (served=false)")
	}
}

const testMultiDocYAML = `apiVersion: v1
kind: Service
metadata:
  name: my-service
spec:
  ports:
    - port: 80
---
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: bars.example.com
spec:
  group: example.com
  names:
    kind: Bar
    plural: bars
    singular: bar
  scope: Cluster
  versions:
    - name: v1
      served: true
      storage: true
      schema:
        openAPIV3Schema:
          type: object
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: my-config
`

func TestParseCRDsMultiDoc(t *testing.T) {
	schemas, err := parseCRDs(nopLog, []byte(testMultiDocYAML))
	if err != nil {
		t.Fatalf("parseCRDs: %v", err)
	}

	if len(schemas) != 1 {
		t.Fatalf("expected 1 schema (only the CRD), got %d", len(schemas))
	}

	if schemas[0].Kind != "Bar" {
		t.Errorf("kind = %q, want %q", schemas[0].Kind, "Bar")
	}
}

func TestParseCRDsEmpty(t *testing.T) {
	schemas, err := parseCRDs(nopLog, []byte(""))
	if err != nil {
		t.Fatalf("parseCRDs on empty input: %v", err)
	}
	if len(schemas) != 0 {
		t.Errorf("expected 0 schemas, got %d", len(schemas))
	}
}

func TestParseCRDsNoCRDs(t *testing.T) {
	yaml := `apiVersion: v1
kind: Service
metadata:
  name: svc
spec:
  ports:
    - port: 80
`
	schemas, err := parseCRDs(nopLog, []byte(yaml))
	if err != nil {
		t.Fatalf("parseCRDs: %v", err)
	}
	if len(schemas) != 0 {
		t.Errorf("expected 0 schemas from non-CRD input, got %d", len(schemas))
	}
}

func TestParseCRDsNoSchema(t *testing.T) {
	yaml := `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: noschemas.example.com
spec:
  group: example.com
  names:
    kind: NoSchema
    plural: noschemas
    singular: noschema
  scope: Namespaced
  versions:
    - name: v1
      served: true
      storage: true
`
	schemas, err := parseCRDs(nopLog, []byte(yaml))
	if err != nil {
		t.Fatalf("parseCRDs: %v", err)
	}
	if len(schemas) != 0 {
		t.Errorf("expected 0 schemas (CRD without openAPIV3Schema), got %d", len(schemas))
	}
}

func TestParseCRDsMalformedYAML(t *testing.T) {
	schemas, err := parseCRDs(nopLog, []byte(`{{not valid yaml at all`))
	if err != nil {
		t.Fatalf("parseCRDs should not return error on malformed input, got: %v", err)
	}
	if len(schemas) != 0 {
		t.Errorf("expected 0 schemas from malformed YAML, got %d", len(schemas))
	}
}

func TestParseCRDsMalformedCRDAmongValid(t *testing.T) {
	// A valid CRD followed by a document that claims to be a CRD but has
	// broken spec structure -- the parser should extract the valid one and
	// skip the broken one.
	yaml := `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: goods.example.com
spec:
  group: example.com
  names:
    kind: Good
    plural: goods
    singular: good
  scope: Namespaced
  versions:
    - name: v1
      served: true
      storage: true
      schema:
        openAPIV3Schema:
          type: object
---
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: bads.example.com
spec:
  group: example.com
  names:
    kind: Bad
    plural: bads
    singular: bad
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
              type: INVALID_TYPE_THAT_SHOULD_NOT_CRASH
`
	schemas, err := parseCRDs(nopLog, []byte(yaml))
	if err != nil {
		t.Fatalf("parseCRDs should not return error, got: %v", err)
	}

	// Both should still parse -- the schema content is passed through as-is,
	// validation is kubeconform's job, not ours.
	if len(schemas) != 2 {
		t.Fatalf("expected 2 schemas (we extract, not validate), got %d", len(schemas))
	}
}

func TestParseCRDsTruncatedDocument(t *testing.T) {
	// A CRD that is cut off mid-document
	yaml := `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: truncated.example.com
spec:
  group: example.com
  names:
    kind: Truncated
    plural: truncated
  versions:
    - name: v1
      served: true
      storage: true
      schema:
        openAPIV3Sc`

	schemas, err := parseCRDs(nopLog, []byte(yaml))
	if err != nil {
		t.Fatalf("parseCRDs should not return error on truncated input, got: %v", err)
	}
	// Truncated doc won't have a valid schema, so 0 schemas
	if len(schemas) != 0 {
		t.Errorf("expected 0 schemas from truncated CRD, got %d", len(schemas))
	}
}

func TestParseCRDsMissingGroup(t *testing.T) {
	yaml := `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: nogroup.example.com
spec:
  names:
    kind: NoGroup
    plural: nogroups
    singular: nogroup
  scope: Namespaced
  versions:
    - name: v1
      served: true
      storage: true
      schema:
        openAPIV3Schema:
          type: object
`
	schemas, err := parseCRDs(nopLog, []byte(yaml))
	if err != nil {
		t.Fatalf("parseCRDs: %v", err)
	}
	if len(schemas) != 0 {
		t.Errorf("expected 0 schemas from CRD with no group, got %d", len(schemas))
	}
}
