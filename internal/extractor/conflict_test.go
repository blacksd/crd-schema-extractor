package extractor

import (
	"testing"
)

func TestDedup(t *testing.T) {
	schemas := []CRDSchema{
		{Group: "example.com", Kind: "Foo", APIVersion: "v1", Schema: []byte(`{}`)},
		{Group: "example.com", Kind: "Foo", APIVersion: "v1", Schema: []byte(`{}`)},
		{Group: "example.com", Kind: "Bar", APIVersion: "v1", Schema: []byte(`{}`)},
	}

	result := dedup(schemas)
	if len(result) != 2 {
		t.Fatalf("expected 2 deduplicated schemas, got %d", len(result))
	}
}

// --- Conflict detection tests ---

func TestDetectConflictsNone(t *testing.T) {
	schemas := []CRDSchema{
		{Group: "a.io", Kind: "Foo", APIVersion: "v1", Schema: []byte(`{"type":"object"}`), SourceName: "src-a"},
		{Group: "b.io", Kind: "Bar", APIVersion: "v1", Schema: []byte(`{"type":"object"}`), SourceName: "src-b"},
	}

	conflicts := DetectConflicts(nopLog, schemas)
	if len(conflicts) != 0 {
		t.Errorf("expected no conflicts, got %d", len(conflicts))
	}
}

func TestDetectConflictsIdenticalDuplicates(t *testing.T) {
	schemas := []CRDSchema{
		{Group: "a.io", Kind: "Foo", APIVersion: "v1", Schema: []byte(`{"type":"object"}`), SourceName: "src-a"},
		{Group: "a.io", Kind: "Foo", APIVersion: "v1", Schema: []byte(`{"type":"object"}`), SourceName: "src-b"},
	}

	conflicts := DetectConflicts(nopLog, schemas)
	if len(conflicts) != 0 {
		t.Errorf("identical duplicates should not be conflicts, got %d", len(conflicts))
	}
}

func TestDetectConflictsIdenticalIgnoresFormatting(t *testing.T) {
	schemas := []CRDSchema{
		{Group: "a.io", Kind: "Foo", APIVersion: "v1", Schema: []byte(`{"type": "object"}`), SourceName: "src-a"},
		{Group: "a.io", Kind: "Foo", APIVersion: "v1", Schema: []byte(`{"type":"object"}`), SourceName: "src-b"},
	}

	conflicts := DetectConflicts(nopLog, schemas)
	if len(conflicts) != 0 {
		t.Errorf("formatting-only differences should not be conflicts, got %d", len(conflicts))
	}
}

func TestDetectConflictsDifferentContent(t *testing.T) {
	schemas := []CRDSchema{
		{Group: "a.io", Kind: "Foo", APIVersion: "v1", Schema: []byte(`{"type":"object"}`), SourceName: "src-a"},
		{Group: "a.io", Kind: "Foo", APIVersion: "v1", Schema: []byte(`{"type":"string"}`), SourceName: "src-b"},
	}

	conflicts := DetectConflicts(nopLog, schemas)
	if len(conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %d", len(conflicts))
	}

	key := "a.io/Foo/v1"
	entries, ok := conflicts[key]
	if !ok {
		t.Fatalf("expected conflict for key %q", key)
	}
	if len(entries) != 2 {
		t.Errorf("expected 2 conflicting entries, got %d", len(entries))
	}
	if entries[0].SourceName != "src-a" {
		t.Errorf("entries[0].SourceName = %q, want %q", entries[0].SourceName, "src-a")
	}
	if entries[1].SourceName != "src-b" {
		t.Errorf("entries[1].SourceName = %q, want %q", entries[1].SourceName, "src-b")
	}
}

func TestDetectConflictsMultiple(t *testing.T) {
	schemas := []CRDSchema{
		{Group: "a.io", Kind: "Foo", APIVersion: "v1", Schema: []byte(`{"a":1}`), SourceName: "src-a"},
		{Group: "a.io", Kind: "Foo", APIVersion: "v1", Schema: []byte(`{"a":2}`), SourceName: "src-b"},
		{Group: "b.io", Kind: "Bar", APIVersion: "v1", Schema: []byte(`{"b":1}`), SourceName: "src-a"},
		{Group: "b.io", Kind: "Bar", APIVersion: "v1", Schema: []byte(`{"b":2}`), SourceName: "src-c"},
		{Group: "c.io", Kind: "Baz", APIVersion: "v1", Schema: []byte(`{"c":1}`), SourceName: "src-a"}, // no conflict
	}

	conflicts := DetectConflicts(nopLog, schemas)
	if len(conflicts) != 2 {
		t.Fatalf("expected 2 conflicts, got %d", len(conflicts))
	}
}

func TestDetectConflictsThreeSources(t *testing.T) {
	schemas := []CRDSchema{
		{Group: "a.io", Kind: "Foo", APIVersion: "v1", Schema: []byte(`{"v":1}`), SourceName: "src-a"},
		{Group: "a.io", Kind: "Foo", APIVersion: "v1", Schema: []byte(`{"v":2}`), SourceName: "src-b"},
		{Group: "a.io", Kind: "Foo", APIVersion: "v1", Schema: []byte(`{"v":3}`), SourceName: "src-c"},
	}

	conflicts := DetectConflicts(nopLog, schemas)
	if len(conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %d", len(conflicts))
	}
	entries := conflicts["a.io/Foo/v1"]
	if len(entries) != 3 {
		t.Errorf("expected 3 entries in conflict, got %d", len(entries))
	}
}

func TestDetectConflictsEmpty(t *testing.T) {
	conflicts := DetectConflicts(nopLog, nil)
	if len(conflicts) != 0 {
		t.Errorf("expected no conflicts for nil input, got %d", len(conflicts))
	}
}

// --- SchemaKey test ---

func TestSchemaKey(t *testing.T) {
	s := CRDSchema{Group: "example.com", Kind: "Foo", APIVersion: "v1"}
	key := SchemaKey(s)
	if key != "example.com/Foo/v1" {
		t.Errorf("SchemaKey = %q, want %q", key, "example.com/Foo/v1")
	}
}

// --- jsonEqual tests ---

func TestJSONEqualIdentical(t *testing.T) {
	if !jsonEqual([]byte(`{"a":1}`), []byte(`{"a":1}`)) {
		t.Error("identical JSON should be equal")
	}
}

func TestJSONEqualDifferentFormatting(t *testing.T) {
	if !jsonEqual([]byte(`{"a": 1, "b": 2}`), []byte(`{"b":2,"a":1}`)) {
		t.Error("same content with different formatting/key order should be equal")
	}
}

func TestJSONEqualDifferentContent(t *testing.T) {
	if jsonEqual([]byte(`{"a":1}`), []byte(`{"a":2}`)) {
		t.Error("different content should not be equal")
	}
}

func TestJSONEqualInvalidJSON(t *testing.T) {
	if jsonEqual([]byte(`not json`), []byte(`{"a":1}`)) {
		t.Error("invalid JSON should not be equal")
	}
	if jsonEqual([]byte(`{"a":1}`), []byte(`not json`)) {
		t.Error("invalid JSON should not be equal")
	}
}
