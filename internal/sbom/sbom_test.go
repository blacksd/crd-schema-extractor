package sbom

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	cdx "github.com/CycloneDX/cyclonedx-go"

	"github.com/blacksd/crd-schema-extractor/internal/source"
)

var serialNumberRe = regexp.MustCompile(`^urn:uuid:[\da-f]{8}-[\da-f]{4}-[\da-f]{4}-[\da-f]{4}-[\da-f]{12}$`)

func decode(t *testing.T, data []byte) *cdx.BOM {
	t.Helper()
	bom := cdx.NewBOM()
	decoder := cdx.NewBOMDecoder(bytes.NewReader(data), cdx.BOMFileFormatJSON)
	if err := decoder.Decode(bom); err != nil {
		t.Fatalf("decode SBOM: %v", err)
	}
	return bom
}

func TestGenerate(t *testing.T) {
	sources := []source.Source{
		{
			Name:     "test-operator",
			Type:     "helm",
			Repo:     "https://charts.example.io",
			Chart:    "test-operator",
			Version:  "v1.0.0",
			License:  "Apache-2.0",
			Homepage: "https://example.com",
		},
		{
			Name:    "other-thing",
			Type:    "url",
			URL:     "https://example.com/crds.yaml",
			Version: "v2.0.0",
		},
	}

	data, err := Generate(sources, "2026-01-01T00:00:00Z", nil)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	bom := decode(t, data)

	if bom.BOMFormat != "CycloneDX" {
		t.Errorf("bomFormat = %q, want %q", bom.BOMFormat, "CycloneDX")
	}
	if bom.SpecVersion != cdx.SpecVersion1_5 {
		t.Errorf("specVersion = %q, want %q", bom.SpecVersion, cdx.SpecVersion1_5)
	}
	if !serialNumberRe.MatchString(bom.SerialNumber) {
		t.Errorf("serialNumber %q does not match urn:uuid format", bom.SerialNumber)
	}
	if bom.Version != 1 {
		t.Errorf("version = %d, want 1", bom.Version)
	}
	if bom.Metadata == nil || bom.Metadata.Timestamp != "2026-01-01T00:00:00Z" {
		t.Errorf("metadata.timestamp = %q, want %q", bom.Metadata.Timestamp, "2026-01-01T00:00:00Z")
	}

	// Tool is declared as a component in spec 1.5+
	if bom.Metadata.Tools == nil || bom.Metadata.Tools.Components == nil || len(*bom.Metadata.Tools.Components) != 1 {
		t.Fatal("expected 1 tool component in metadata")
	}
	toolComp := (*bom.Metadata.Tools.Components)[0]
	if toolComp.Name != "crd-schemas" {
		t.Errorf("tool name = %q, want %q", toolComp.Name, "crd-schemas")
	}

	if bom.Components == nil || len(*bom.Components) != 2 {
		t.Fatalf("expected 2 components, got %v", bom.Components)
	}
	components := *bom.Components

	// Components are sorted by name: "other-thing" before "test-operator"
	c0 := components[0]
	if c0.Name != "other-thing" {
		t.Errorf("components[0].name = %q, want %q", c0.Name, "other-thing")
	}
	if c0.Version != "v2.0.0" {
		t.Errorf("components[0].version = %q, want %q", c0.Version, "v2.0.0")
	}
	if c0.Type != cdx.ComponentTypeLibrary {
		t.Errorf("components[0].type = %q, want %q", c0.Type, cdx.ComponentTypeLibrary)
	}

	// Verify properties on url-type source
	if c0.Properties == nil {
		t.Fatal("expected properties on components[0]")
	}
	propMap := propsToMap(*c0.Properties)
	if propMap["crd-schemas:source:type"] != "url" {
		t.Errorf("source:type = %q, want %q", propMap["crd-schemas:source:type"], "url")
	}
	if propMap["crd-schemas:source:url"] != "https://example.com/crds.yaml" {
		t.Errorf("source:url = %q, want %q", propMap["crd-schemas:source:url"], "https://example.com/crds.yaml")
	}

	// Second component (alphabetically): has license, homepage, helm properties
	c1 := components[1]
	if c1.Name != "test-operator" {
		t.Errorf("components[1].name = %q, want %q", c1.Name, "test-operator")
	}
	if c1.Version != "v1.0.0" {
		t.Errorf("components[1].version = %q, want %q", c1.Version, "v1.0.0")
	}
	if c1.Licenses == nil || len(*c1.Licenses) != 1 || (*c1.Licenses)[0].License.ID != "Apache-2.0" {
		t.Errorf("components[1].licenses unexpected: %+v", c1.Licenses)
	}
	if c1.ExternalReferences == nil || len(*c1.ExternalReferences) != 1 || (*c1.ExternalReferences)[0].URL != "https://example.com" {
		t.Errorf("components[1].externalReferences unexpected: %+v", c1.ExternalReferences)
	}

	// Verify helm properties
	if c1.Properties == nil {
		t.Fatal("expected properties on components[1]")
	}
	propMap = propsToMap(*c1.Properties)
	if propMap["crd-schemas:source:type"] != "helm" {
		t.Errorf("source:type = %q, want %q", propMap["crd-schemas:source:type"], "helm")
	}
	if propMap["crd-schemas:source:repo"] != "https://charts.example.io" {
		t.Errorf("source:repo = %q, want %q", propMap["crd-schemas:source:repo"], "https://charts.example.io")
	}
	if propMap["crd-schemas:source:chart"] != "test-operator" {
		t.Errorf("source:chart = %q, want %q", propMap["crd-schemas:source:chart"], "test-operator")
	}
}

func TestGenerateWithExistingState(t *testing.T) {
	sources := []source.Source{{Name: "test", Type: "helm", Version: "v2.0.0"}}
	existing := &State{
		SerialNumber: "urn:uuid:12345678-1234-4234-8234-123456789abc",
		Version:      3,
		Components:   []ComponentState{{Name: "test", Version: "v1.0.0"}},
	}

	data, err := Generate(sources, "2026-01-01T00:00:00Z", existing)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	bom := decode(t, data)

	if bom.SerialNumber != existing.SerialNumber {
		t.Errorf("serialNumber = %q, want %q (preserved)", bom.SerialNumber, existing.SerialNumber)
	}
	if bom.Version != 4 {
		t.Errorf("version = %d, want 4 (bumped from 3)", bom.Version)
	}
}

func TestGenerateEmpty(t *testing.T) {
	data, err := Generate(nil, "2026-01-01T00:00:00Z", nil)
	if err != nil {
		t.Fatalf("Generate with nil sources: %v", err)
	}

	bom := decode(t, data)

	if !serialNumberRe.MatchString(bom.SerialNumber) {
		t.Errorf("serialNumber %q does not match urn:uuid format", bom.SerialNumber)
	}
	if bom.Version != 1 {
		t.Errorf("version = %d, want 1", bom.Version)
	}
	if bom.Components != nil && len(*bom.Components) != 0 {
		t.Errorf("expected nil/empty components for empty input, got %d", len(*bom.Components))
	}
}

func TestGenerateValidJSON(t *testing.T) {
	sources := []source.Source{{Name: "test", Type: "helm", Version: "v1.0.0"}}
	data, err := Generate(sources, "2026-01-01T00:00:00Z", nil)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if !json.Valid(data) {
		t.Error("output is not valid JSON")
	}
}

func TestLoadExistingMissing(t *testing.T) {
	state, err := LoadExisting(filepath.Join(t.TempDir(), "nonexistent.json"))
	if err != nil {
		t.Fatalf("LoadExisting: %v", err)
	}
	if state != nil {
		t.Errorf("expected nil state for missing file, got %+v", state)
	}
}

func TestLoadExistingCorrupt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "corrupt.json")
	if err := os.WriteFile(path, []byte("not valid json{{{"), 0644); err != nil {
		t.Fatal(err)
	}

	state, err := LoadExisting(path)
	if err != nil {
		t.Fatalf("LoadExisting: %v", err)
	}
	if state != nil {
		t.Errorf("expected nil state for corrupt file, got %+v", state)
	}
}

func TestLoadExistingValid(t *testing.T) {
	sources := []source.Source{
		{Name: "alpha", Type: "helm", Version: "v1.0.0"},
		{Name: "beta", Type: "url", Version: "v2.0.0"},
	}
	data, err := Generate(sources, "2026-01-01T00:00:00Z", nil)
	if err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "sbom.cdx.json")
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}

	state, err := LoadExisting(path)
	if err != nil {
		t.Fatalf("LoadExisting: %v", err)
	}
	if state == nil {
		t.Fatal("expected non-nil state")
	}
	if !serialNumberRe.MatchString(state.SerialNumber) {
		t.Errorf("serialNumber %q does not match urn:uuid format", state.SerialNumber)
	}
	if state.Version != 1 {
		t.Errorf("version = %d, want 1", state.Version)
	}
	if len(state.Components) != 2 {
		t.Fatalf("expected 2 components, got %d", len(state.Components))
	}
	// Components are sorted alphabetically by Generate
	if state.Components[0].Name != "alpha" || state.Components[0].Version != "v1.0.0" {
		t.Errorf("components[0] = %+v, want {alpha v1.0.0}", state.Components[0])
	}
	if state.Components[1].Name != "beta" || state.Components[1].Version != "v2.0.0" {
		t.Errorf("components[1] = %+v, want {beta v2.0.0}", state.Components[1])
	}
}

func TestLoadExistingRoundTrip(t *testing.T) {
	// Generate → write → load → generate again: serial number preserved, version bumped
	sources := []source.Source{{Name: "test", Type: "helm", Version: "v1.0.0"}}

	data1, err := Generate(sources, "2026-01-01T00:00:00Z", nil)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "sbom.cdx.json")
	if err := os.WriteFile(path, data1, 0644); err != nil {
		t.Fatal(err)
	}

	state, err := LoadExisting(path)
	if err != nil {
		t.Fatal(err)
	}

	// Bump version
	sources[0].Version = "v2.0.0"
	data2, err := Generate(sources, "2026-02-01T00:00:00Z", state)
	if err != nil {
		t.Fatal(err)
	}

	bom := decode(t, data2)
	if bom.SerialNumber != state.SerialNumber {
		t.Errorf("serialNumber changed: %q → %q", state.SerialNumber, bom.SerialNumber)
	}
	if bom.Version != 2 {
		t.Errorf("version = %d, want 2", bom.Version)
	}
}

func TestHasChangedNilState(t *testing.T) {
	sources := []source.Source{{Name: "test", Version: "v1.0.0"}}
	if !HasChanged(sources, nil) {
		t.Error("HasChanged(sources, nil) = false, want true")
	}
}

func TestHasChangedSameVersions(t *testing.T) {
	sources := []source.Source{
		{Name: "alpha", Version: "v1.0.0"},
		{Name: "beta", Version: "v2.0.0"},
	}
	existing := &State{
		Components: []ComponentState{
			{Name: "alpha", Version: "v1.0.0"},
			{Name: "beta", Version: "v2.0.0"},
		},
	}
	if HasChanged(sources, existing) {
		t.Error("HasChanged = true, want false for identical sources")
	}
}

func TestHasChangedDifferentVersions(t *testing.T) {
	sources := []source.Source{
		{Name: "alpha", Version: "v1.1.0"},
		{Name: "beta", Version: "v2.0.0"},
	}
	existing := &State{
		Components: []ComponentState{
			{Name: "alpha", Version: "v1.0.0"},
			{Name: "beta", Version: "v2.0.0"},
		},
	}
	if !HasChanged(sources, existing) {
		t.Error("HasChanged = false, want true for version bump")
	}
}

func TestHasChangedAddedSource(t *testing.T) {
	sources := []source.Source{
		{Name: "alpha", Version: "v1.0.0"},
		{Name: "beta", Version: "v2.0.0"},
	}
	existing := &State{
		Components: []ComponentState{
			{Name: "alpha", Version: "v1.0.0"},
		},
	}
	if !HasChanged(sources, existing) {
		t.Error("HasChanged = false, want true for added source")
	}
}

func TestHasChangedRemovedSource(t *testing.T) {
	sources := []source.Source{
		{Name: "alpha", Version: "v1.0.0"},
	}
	existing := &State{
		Components: []ComponentState{
			{Name: "alpha", Version: "v1.0.0"},
			{Name: "beta", Version: "v2.0.0"},
		},
	}
	if !HasChanged(sources, existing) {
		t.Error("HasChanged = false, want true for removed source")
	}
}

func TestNewSerialNumberFormat(t *testing.T) {
	sn := newSerialNumber()
	if !serialNumberRe.MatchString(sn) {
		t.Errorf("newSerialNumber() = %q, does not match urn:uuid format", sn)
	}
}

func TestNewSerialNumberUniqueness(t *testing.T) {
	a := newSerialNumber()
	b := newSerialNumber()
	if a == b {
		t.Errorf("two calls to newSerialNumber() produced identical values: %q", a)
	}
}

// propsToMap converts a slice of CycloneDX properties to a map for easier assertions.
func propsToMap(props []cdx.Property) map[string]string {
	m := make(map[string]string, len(props))
	for _, p := range props {
		m[p.Name] = p.Value
	}
	return m
}
