package extractor

import (
	"strings"

	"github.com/blacksd/crd-schema-extractor/internal/source"
)

// Filter applies include/exclude rules from the source config to a set of
// extracted schemas. Rules can match by:
//   - Kind alone: "GatewayClass"
//   - group/Kind: "gateway.networking.k8s.io/GatewayClass"
//   - group wildcard: "gateway.networking.k8s.io/*"
//
// If Include is non-empty, only matching schemas are kept (allowlist).
// If Exclude is non-empty, matching schemas are removed (denylist).
// Include takes precedence: if both are set, only Include is evaluated.
func Filter(schemas []CRDSchema, src source.Source) []CRDSchema {
	if len(src.Include) == 0 && len(src.Exclude) == 0 {
		return schemas
	}

	if len(src.Include) > 0 {
		var result []CRDSchema
		for _, s := range schemas {
			if matchesAny(s, src.Include) {
				result = append(result, s)
			}
		}
		return result
	}

	// Exclude mode
	var result []CRDSchema
	for _, s := range schemas {
		if !matchesAny(s, src.Exclude) {
			result = append(result, s)
		}
	}
	return result
}

// matchesAny checks if a schema matches any of the given filter patterns.
func matchesAny(s CRDSchema, patterns []string) bool {
	for _, p := range patterns {
		if matchPattern(s, p) {
			return true
		}
	}
	return false
}

// matchPattern checks if a schema matches a single filter pattern.
func matchPattern(s CRDSchema, pattern string) bool {
	if idx := strings.Index(pattern, "/"); idx >= 0 {
		group := pattern[:idx]
		kind := pattern[idx+1:]
		if s.Group != group {
			return false
		}
		return kind == "*" || strings.EqualFold(s.Kind, kind)
	}
	// Kind-only match (case-insensitive)
	return strings.EqualFold(s.Kind, pattern)
}
