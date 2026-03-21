// Package sbom generates per-API-group CycloneDX SBOMs from extracted sources.
package sbom

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"os"
	"sort"

	cdx "github.com/CycloneDX/cyclonedx-go"
	"github.com/rs/zerolog/log"

	"github.com/blacksd/crd-schema-extractor/internal/provenance"
	"github.com/blacksd/crd-schema-extractor/internal/source"
)

// ComponentState captures the identity of a component for change detection.
type ComponentState struct {
	Name    string
	Version string
}

// State holds the relevant fields from an existing SBOM needed for
// serial number preservation and version bumping.
type State struct {
	SerialNumber string
	Version      int
	Components   []ComponentState
}

// LoadExisting reads an existing SBOM file and extracts its state.
// Returns nil, nil if the file does not exist.
// Returns nil, nil (with a warning log) if the file is corrupt or unparseable.
func LoadExisting(path string) (*State, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	bom := cdx.NewBOM()
	decoder := cdx.NewBOMDecoder(f, cdx.BOMFileFormatJSON)
	if err := decoder.Decode(bom); err != nil {
		log.Warn().Err(err).Str("path", path).Msg("corrupt SBOM, treating as fresh")
		return nil, nil
	}

	state := &State{
		SerialNumber: bom.SerialNumber,
		Version:      bom.Version,
	}

	if bom.Components != nil {
		for _, c := range *bom.Components {
			state.Components = append(state.Components, ComponentState{
				Name:    c.Name,
				Version: c.Version,
			})
		}
	}

	return state, nil
}

// HasChanged reports whether the source list differs from the existing SBOM state.
// Returns true if existing is nil (no previous SBOM).
func HasChanged(sources []source.Source, existing *State) bool {
	if existing == nil {
		return true
	}

	current := make([]ComponentState, len(sources))
	for i, src := range sources {
		current[i] = ComponentState{Name: src.Name, Version: src.Version}
	}
	sort.Slice(current, func(i, j int) bool { return current[i].Name < current[j].Name })

	if len(current) != len(existing.Components) {
		return true
	}
	for i := range current {
		if current[i] != existing.Components[i] {
			return true
		}
	}
	return false
}

// newSerialNumber generates a URN UUID v4 using crypto/rand.
func newSerialNumber() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("crypto/rand failed: %v", err))
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("urn:uuid:%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// Generate creates a CycloneDX 1.5 SBOM from a list of sources.
// If existing is non-nil, the serial number is preserved and version is bumped.
// If existing is nil, a fresh serial number and version 1 are used.
func Generate(sources []source.Source, timestamp string, existing *State) ([]byte, error) {
	bom := cdx.NewBOM()

	if existing != nil {
		bom.SerialNumber = existing.SerialNumber
		bom.Version = existing.Version + 1
	} else {
		bom.SerialNumber = newSerialNumber()
		bom.Version = 1
	}

	bom.Metadata = &cdx.Metadata{
		Timestamp: timestamp,
		Tools: &cdx.ToolsChoice{
			Components: &[]cdx.Component{
				{
					Type:    cdx.ComponentTypeApplication,
					Name:    "crd-schemas",
					Version: provenance.ToolVersion,
				},
			},
		},
	}

	var components []cdx.Component
	for _, src := range sources {
		c := cdx.Component{
			Type:    cdx.ComponentTypeLibrary,
			Name:    src.Name,
			Version: src.Version,
		}
		if src.License != "" {
			c.Licenses = &cdx.Licenses{
				cdx.LicenseChoice{
					License: &cdx.License{ID: src.License},
				},
			}
		}

		var refs []cdx.ExternalReference
		if src.Homepage != "" {
			refs = append(refs, cdx.ExternalReference{Type: cdx.ERTypeWebsite, URL: src.Homepage})
		}
		if len(refs) > 0 {
			c.ExternalReferences = &refs
		}

		var props []cdx.Property
		props = append(props, cdx.Property{Name: "crd-schemas:source:type", Value: src.Type})
		if src.Repo != "" {
			props = append(props, cdx.Property{Name: "crd-schemas:source:repo", Value: src.Repo})
		}
		if src.URL != "" {
			props = append(props, cdx.Property{Name: "crd-schemas:source:url", Value: src.URL})
		}
		if src.Chart != "" {
			props = append(props, cdx.Property{Name: "crd-schemas:source:chart", Value: src.Chart})
		}
		c.Properties = &props

		components = append(components, c)
	}

	sort.Slice(components, func(i, j int) bool {
		return components[i].Name < components[j].Name
	})

	if len(components) > 0 {
		bom.Components = &components
	}

	var buf bytes.Buffer
	encoder := cdx.NewBOMEncoder(&buf, cdx.BOMFileFormatJSON)
	encoder.SetPretty(true)
	if err := encoder.EncodeVersion(bom, cdx.SpecVersion1_5); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}
