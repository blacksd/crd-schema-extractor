package extractor

import (
	"strings"
	"testing"
)

func TestStripConditionalWrapper(t *testing.T) {
	input := `{{- if .Values.crds.install }}
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: foos.example.com
spec:
  group: example.com
  names:
    kind: Foo
{{- end }}
`
	result := StripTemplateDirectives([]byte(input))
	output := string(result)

	if strings.Contains(output, "{{") {
		t.Errorf("output still contains directives: %s", output)
	}
	if !strings.Contains(output, "kind: CustomResourceDefinition") {
		t.Error("output missing CRD kind")
	}
	if !strings.Contains(output, "kind: Foo") {
		t.Error("output missing Foo kind")
	}
}

func TestStripMetadataLabels(t *testing.T) {
	input := `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: foos.example.com
  labels:
    app: my-app
    {{- include "chart.labels" . | nindent 4 }}
  annotations:
    cert-manager.io/inject-ca-from: {{ .Values.caInjection }}
spec:
  group: example.com
`
	result := StripTemplateDirectives([]byte(input))
	output := string(result)

	if strings.Contains(output, "{{") {
		t.Errorf("output still contains directives: %s", output)
	}
	if !strings.Contains(output, "app: my-app") {
		t.Error("non-template label should be preserved")
	}
	if !strings.Contains(output, "group: example.com") {
		t.Error("spec should be preserved")
	}
}

func TestStripNoDirectives(t *testing.T) {
	input := `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: foos.example.com
spec:
  group: example.com
`
	result := StripTemplateDirectives([]byte(input))
	output := string(result)

	if output != input {
		t.Errorf("plain YAML should pass through unchanged.\ngot:\n%s\nwant:\n%s", output, input)
	}
}

func TestStripCertManagerPattern(t *testing.T) {
	// Real-world cert-manager CRD pattern: conditional + labels + resource-policy
	input := `{{- if .Values.crds.enabled }}
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: certificates.cert-manager.io
  labels:
    app.kubernetes.io/name: cert-manager
    {{- include "cert-manager.crd-labels" . | nindent 4 }}
  annotations:
    {{- if .Values.crds.keep }}
    "helm.sh/resource-policy": keep
    {{- end }}
spec:
  group: cert-manager.io
  names:
    kind: Certificate
    plural: certificates
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
                secretName:
                  type: string
{{- end }}
`
	result := StripTemplateDirectives([]byte(input))
	output := string(result)

	if strings.Contains(output, "{{") {
		t.Errorf("output still contains directives:\n%s", output)
	}

	// The CRD structure must survive intact
	if !strings.Contains(output, "kind: CustomResourceDefinition") {
		t.Error("missing CRD kind")
	}
	if !strings.Contains(output, "kind: Certificate") {
		t.Error("missing Certificate kind")
	}
	if !strings.Contains(output, "secretName:") {
		t.Error("missing schema property")
	}
	if !strings.Contains(output, "openAPIV3Schema:") {
		t.Error("missing openAPIV3Schema")
	}
}

func TestStripEmptyResult(t *testing.T) {
	// File that is only directives
	input := `{{- if false }}
{{- end }}
`
	result := StripTemplateDirectives([]byte(input))
	output := strings.TrimSpace(string(result))

	if output != "" {
		t.Errorf("expected empty result, got: %q", output)
	}
}

func TestStripMultipleDirectivesOnOneLine(t *testing.T) {
	input := `{{ if .Values.a }}{{ if .Values.b }}
kind: CustomResourceDefinition
{{ end }}{{ end }}
`
	result := StripTemplateDirectives([]byte(input))
	output := string(result)

	if strings.Contains(output, "{{") {
		t.Errorf("output still contains directives: %s", output)
	}
	if !strings.Contains(output, "kind: CustomResourceDefinition") {
		t.Error("CRD kind should be preserved")
	}
}

func TestStripWhitespaceTrimmingVariants(t *testing.T) {
	input := `{{- if .Values.crds.install -}}
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
{{- end -}}
`
	result := StripTemplateDirectives([]byte(input))
	output := string(result)

	if strings.Contains(output, "{{") {
		t.Errorf("output still contains directives: %s", output)
	}
	if !strings.Contains(output, "kind: CustomResourceDefinition") {
		t.Error("CRD kind should be preserved")
	}
}

func TestStripPreservesYAMLStructure(t *testing.T) {
	// Ensure that indentation-sensitive YAML like nested properties survives
	input := `{{- if .Values.crds.install }}
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
spec:
  versions:
    - name: v1
      schema:
        openAPIV3Schema:
          type: object
          properties:
            spec:
              type: object
              properties:
                nested:
                  type: object
                  properties:
                    deep:
                      type: string
{{- end }}
`
	result := StripTemplateDirectives([]byte(input))
	// The stripped result should parse cleanly -- we verify the structure
	// is maintained by checking key indented lines
	output := string(result)

	lines := strings.Split(output, "\n")
	found := false
	for _, l := range lines {
		if strings.Contains(l, "deep:") {
			found = true
			// Verify it still has proper indentation (should have leading spaces)
			if !strings.HasPrefix(l, "                    ") {
				t.Errorf("indentation corrupted for 'deep:': %q", l)
			}
		}
	}
	if !found {
		t.Error("deeply nested property 'deep' not found in output")
	}
}
