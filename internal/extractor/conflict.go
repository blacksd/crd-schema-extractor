package extractor

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/rs/zerolog"
)

// SchemaKey returns the unique identifier for a schema: "group/Kind/apiVersion".
func SchemaKey(s CRDSchema) string {
	return fmt.Sprintf("%s/%s/%s", s.Group, s.Kind, s.APIVersion)
}

func dedup(schemas []CRDSchema) []CRDSchema {
	seen := make(map[string]bool)
	var result []CRDSchema
	for _, s := range schemas {
		key := fmt.Sprintf("%s/%s/%s", s.Group, s.Kind, s.APIVersion)
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, s)
	}
	return result
}

// DetectConflicts checks a collection of schemas from multiple sources for
// conflicting entries: same group/kind/version but different schema content.
// Returns nil if no conflicts are found. Identical duplicates are logged
// as informational messages but not treated as conflicts.
func DetectConflicts(log zerolog.Logger, allSchemas []CRDSchema) []Conflict {
	type entry struct {
		schema     json.RawMessage
		sourceName string
	}

	seen := make(map[string][]entry)
	for _, s := range allSchemas {
		key := SchemaKey(s)
		seen[key] = append(seen[key], entry{schema: s.Schema, sourceName: s.SourceName})
	}

	var conflicts []Conflict
	for key, entries := range seen {
		if len(entries) < 2 {
			continue
		}

		// Check if all entries are identical
		allIdentical := true
		for i := 1; i < len(entries); i++ {
			if !jsonEqual(entries[0].schema, entries[i].schema) {
				allIdentical = false
				break
			}
		}

		parts := strings.SplitN(key, "/", 3)
		sources := make([]string, len(entries))
		for i, e := range entries {
			sources[i] = e.sourceName
		}

		if allIdentical {
			log.Info().Str("schema", key).Strs("sources", sources).Msg("duplicate schema with identical content")
			continue
		}

		conflicts = append(conflicts, Conflict{
			Group:      parts[0],
			Kind:       parts[1],
			APIVersion: parts[2],
			Sources:    sources,
		})
	}

	return conflicts
}

// jsonEqual compares two JSON byte slices for semantic equality
// (ignoring formatting differences).
func jsonEqual(a, b json.RawMessage) bool {
	var va, vb interface{}
	if err := json.Unmarshal(a, &va); err != nil {
		return false
	}
	if err := json.Unmarshal(b, &vb); err != nil {
		return false
	}
	// Re-marshal to canonical form for comparison
	ca, err := json.Marshal(va)
	if err != nil {
		return false
	}
	cb, err := json.Marshal(vb)
	if err != nil {
		return false
	}
	return string(ca) == string(cb)
}
