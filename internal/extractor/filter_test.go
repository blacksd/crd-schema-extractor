package extractor

import (
	"testing"

	"github.com/blacksd/crd-schema-extractor/internal/source"
)

func testSchemas() []CRDSchema {
	return []CRDSchema{
		{Group: "gateway.networking.k8s.io", Kind: "GatewayClass", APIVersion: "v1"},
		{Group: "gateway.networking.k8s.io", Kind: "Gateway", APIVersion: "v1"},
		{Group: "gateway.networking.k8s.io", Kind: "HTTPRoute", APIVersion: "v1"},
		{Group: "gateway.networking.k8s.io", Kind: "TCPRoute", APIVersion: "v1alpha2"},
		{Group: "gateway.networking.k8s.io", Kind: "TLSRoute", APIVersion: "v1alpha2"},
		{Group: "example.com", Kind: "Foo", APIVersion: "v1"},
	}
}

func schemaKinds(schemas []CRDSchema) []string {
	var kinds []string
	for _, s := range schemas {
		kinds = append(kinds, s.Kind)
	}
	return kinds
}

func containsKind(schemas []CRDSchema, kind string) bool {
	for _, s := range schemas {
		if s.Kind == kind {
			return true
		}
	}
	return false
}

func TestFilterNoRules(t *testing.T) {
	schemas := testSchemas()
	src := source.Source{Name: "test"}

	result := Filter(schemas, src)
	if len(result) != len(schemas) {
		t.Errorf("no rules: expected %d schemas, got %d", len(schemas), len(result))
	}
}

func TestFilterIncludeByKind(t *testing.T) {
	schemas := testSchemas()
	src := source.Source{
		Name:    "test",
		Include: []string{"TCPRoute", "TLSRoute"},
	}

	result := Filter(schemas, src)
	if len(result) != 2 {
		t.Fatalf("expected 2 schemas, got %d: %v", len(result), schemaKinds(result))
	}
	if !containsKind(result, "TCPRoute") {
		t.Error("expected TCPRoute in result")
	}
	if !containsKind(result, "TLSRoute") {
		t.Error("expected TLSRoute in result")
	}
}

func TestFilterIncludeByGroupKind(t *testing.T) {
	schemas := testSchemas()
	src := source.Source{
		Name:    "test",
		Include: []string{"gateway.networking.k8s.io/GatewayClass", "example.com/Foo"},
	}

	result := Filter(schemas, src)
	if len(result) != 2 {
		t.Fatalf("expected 2 schemas, got %d: %v", len(result), schemaKinds(result))
	}
	if !containsKind(result, "GatewayClass") {
		t.Error("expected GatewayClass in result")
	}
	if !containsKind(result, "Foo") {
		t.Error("expected Foo in result")
	}
}

func TestFilterIncludeByGroupWildcard(t *testing.T) {
	schemas := testSchemas()
	src := source.Source{
		Name:    "test",
		Include: []string{"gateway.networking.k8s.io/*"},
	}

	result := Filter(schemas, src)
	if len(result) != 5 {
		t.Fatalf("expected 5 gateway schemas, got %d: %v", len(result), schemaKinds(result))
	}
	if containsKind(result, "Foo") {
		t.Error("Foo from example.com should not be included")
	}
}

func TestFilterIncludeCaseInsensitive(t *testing.T) {
	schemas := testSchemas()
	src := source.Source{
		Name:    "test",
		Include: []string{"gatewayclass", "tcproute"},
	}

	result := Filter(schemas, src)
	if len(result) != 2 {
		t.Fatalf("expected 2 schemas (case-insensitive), got %d: %v", len(result), schemaKinds(result))
	}
}

func TestFilterExcludeByKind(t *testing.T) {
	schemas := testSchemas()
	src := source.Source{
		Name:    "test",
		Exclude: []string{"TCPRoute", "TLSRoute"},
	}

	result := Filter(schemas, src)
	if len(result) != 4 {
		t.Fatalf("expected 4 schemas, got %d: %v", len(result), schemaKinds(result))
	}
	if containsKind(result, "TCPRoute") {
		t.Error("TCPRoute should have been excluded")
	}
	if containsKind(result, "TLSRoute") {
		t.Error("TLSRoute should have been excluded")
	}
}

func TestFilterExcludeByGroupWildcard(t *testing.T) {
	schemas := testSchemas()
	src := source.Source{
		Name:    "test",
		Exclude: []string{"gateway.networking.k8s.io/*"},
	}

	result := Filter(schemas, src)
	if len(result) != 1 {
		t.Fatalf("expected 1 schema (only Foo), got %d: %v", len(result), schemaKinds(result))
	}
	if result[0].Kind != "Foo" {
		t.Errorf("expected Foo, got %s", result[0].Kind)
	}
}

func TestFilterExcludeByGroupKind(t *testing.T) {
	schemas := testSchemas()
	src := source.Source{
		Name:    "test",
		Exclude: []string{"gateway.networking.k8s.io/GatewayClass"},
	}

	result := Filter(schemas, src)
	if len(result) != 5 {
		t.Fatalf("expected 5 schemas, got %d: %v", len(result), schemaKinds(result))
	}
	if containsKind(result, "GatewayClass") {
		t.Error("GatewayClass should have been excluded")
	}
}

func TestFilterIncludeTakesPrecedenceOverExclude(t *testing.T) {
	schemas := testSchemas()
	src := source.Source{
		Name:    "test",
		Include: []string{"Foo"},
		Exclude: []string{"Foo"}, // should be ignored
	}

	result := Filter(schemas, src)
	if len(result) != 1 {
		t.Fatalf("include should take precedence: expected 1 schema, got %d", len(result))
	}
	if result[0].Kind != "Foo" {
		t.Errorf("expected Foo, got %s", result[0].Kind)
	}
}

func TestFilterIncludeNoMatch(t *testing.T) {
	schemas := testSchemas()
	src := source.Source{
		Name:    "test",
		Include: []string{"NonExistentKind"},
	}

	result := Filter(schemas, src)
	if len(result) != 0 {
		t.Errorf("expected 0 schemas for non-matching include, got %d", len(result))
	}
}

func TestFilterExcludeGroupDoesNotMatchOtherGroup(t *testing.T) {
	schemas := testSchemas()
	src := source.Source{
		Name:    "test",
		Exclude: []string{"other.group.io/GatewayClass"},
	}

	result := Filter(schemas, src)
	if len(result) != len(schemas) {
		t.Errorf("wrong group should not match: expected %d, got %d", len(schemas), len(result))
	}
}

// --- matchPattern tests ---

func TestMatchPatternKindOnly(t *testing.T) {
	s := CRDSchema{Group: "example.com", Kind: "Foo"}
	if !matchPattern(s, "Foo") {
		t.Error("should match kind 'Foo'")
	}
	if matchPattern(s, "Bar") {
		t.Error("should not match kind 'Bar'")
	}
}

func TestMatchPatternKindCaseInsensitive(t *testing.T) {
	s := CRDSchema{Group: "example.com", Kind: "GatewayClass"}
	if !matchPattern(s, "gatewayclass") {
		t.Error("should match case-insensitively")
	}
	if !matchPattern(s, "GATEWAYCLASS") {
		t.Error("should match case-insensitively")
	}
}

func TestMatchPatternGroupKind(t *testing.T) {
	s := CRDSchema{Group: "example.com", Kind: "Foo"}
	if !matchPattern(s, "example.com/Foo") {
		t.Error("should match group/kind")
	}
	if matchPattern(s, "other.com/Foo") {
		t.Error("should not match wrong group")
	}
	if matchPattern(s, "example.com/Bar") {
		t.Error("should not match wrong kind")
	}
}

func TestMatchPatternGroupKindCaseInsensitive(t *testing.T) {
	s := CRDSchema{Group: "example.com", Kind: "GatewayClass"}
	if !matchPattern(s, "example.com/gatewayclass") {
		t.Error("kind part should be case-insensitive")
	}
}

func TestMatchPatternGroupWildcard(t *testing.T) {
	s := CRDSchema{Group: "example.com", Kind: "Foo"}
	if !matchPattern(s, "example.com/*") {
		t.Error("should match group wildcard")
	}
	if matchPattern(s, "other.com/*") {
		t.Error("should not match wrong group with wildcard")
	}
}
