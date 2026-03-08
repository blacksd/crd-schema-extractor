package extractor

import (
	"bytes"
	"regexp"
)

// directiveRe matches Go template directives: {{ ... }}
// Uses non-greedy matching within single lines.
var directiveRe = regexp.MustCompile(`\{\{-?\s*.*?\s*-?\}\}`)

// lineOnlyDirectiveRe matches lines that contain only template directives
// (possibly multiple) and whitespace.
var lineOnlyDirectiveRe = regexp.MustCompile(`(?m)^\s*(\{\{-?\s*.*?\s*-?\}\}\s*)+$\n?`)

// StripTemplateDirectives removes Go template directives from YAML content,
// enabling CRD extraction from Helm template files without running helm template.
//
// Handles two patterns:
//  1. Line-only directives: lines containing only {{ ... }} (with optional whitespace)
//     are removed entirely (including the newline).
//  2. Inline directives: {{ ... }} embedded within YAML content
//     are replaced with empty string.
//
// This is safe for CRD files because template directives in real-world Helm charts
// only appear in conditional wrappers and metadata labels, never inside the
// openAPIV3Schema specification.
func StripTemplateDirectives(data []byte) []byte {
	// First pass: remove lines that consist entirely of directives.
	// This handles {{- if .Values.crds.install }}, {{- end }}, etc.
	result := lineOnlyDirectiveRe.ReplaceAll(data, nil)

	// Second pass: remove any remaining inline directives.
	// This handles cases like annotations with embedded template calls.
	result = directiveRe.ReplaceAll(result, nil)

	// Clean up: remove any resulting empty lines that might cause YAML issues.
	// Consecutive blank lines -> single blank line.
	result = collapseBlankLines(result)

	return result
}

// collapseBlankLines reduces runs of consecutive blank lines to a single blank line.
func collapseBlankLines(data []byte) []byte {
	var buf bytes.Buffer
	prevBlank := false

	for _, line := range bytes.SplitAfter(data, []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		blank := len(bytes.TrimSpace(line)) == 0
		if blank && prevBlank {
			continue
		}
		prevBlank = blank
		buf.Write(line)
	}

	return buf.Bytes()
}
