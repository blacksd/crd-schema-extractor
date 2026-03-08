# crd-schema-extractor

Extracts JSON schemas from Kubernetes CustomResourceDefinitions (CRDs) published by upstream operators and controllers.

The extractor reads source configuration files, fetches CRDs from Helm charts (HTTP and OCI) or raw URLs, extracts the `openAPIV3Schema` from each served CRD version, and writes standalone JSON Schema files. Each schema includes provenance metadata and a CycloneDX SBOM for the full run.

## Requirements

- [Nix](https://nixos.org/) (recommended) -- provides Go and all tooling via `nix develop`
- Or manually: Go 1.25+

No external tools required at runtime. Helm chart fetching (both HTTP repos and OCI registries) and tarball extraction are implemented in pure Go using [oras-go](https://github.com/oras-project/oras-go).

## Quick start

```bash
# Enter the dev shell (provides go, check-jsonschema, yq, goreleaser)
nix develop

# Run all tests
go test ./...

# Build via Nix
nix build
./result/bin/crd-schema-extractor version
```

## Usage

The CLI accepts a path argument (directory of YAML files or a single file) and defaults to `sources/` when omitted.

```bash
# Extract schemas from all configured sources (default: sources/ -> schemas/)
crd-schema-extractor

# Extract from a specific directory or file
crd-schema-extractor extract sources/
crd-schema-extractor extract sources/cert-manager.io.yaml

# Extract a single source with debug logging
crd-schema-extractor extract sources/cert-manager.io.yaml --debug

# Custom output directory
crd-schema-extractor extract sources/ -o /tmp/schemas

# Parallel fetching (default: 4 concurrent sources)
crd-schema-extractor extract sources/ -p 8

# Fetch only (download charts/manifests to output dir, skip extraction)
crd-schema-extractor extract sources/cert-manager.io.yaml --fetch-only

# Validate source config files
crd-schema-extractor validate sources/
crd-schema-extractor validate sources/cert-manager.io.yaml

# Print version
crd-schema-extractor version
```

When running from source with `go run`:

```bash
go run ./cmd/crd-schema-extractor/ extract sources/cert-manager.io.yaml --debug
go run ./cmd/crd-schema-extractor/ validate sources/
```

## How it works

1. Source configs in `sources/*.yaml` declare upstream CRD locations (Helm chart or URL)
2. The extractor fetches sources in parallel (configurable via `--parallel`), scans for CRD documents, and extracts the `openAPIV3Schema` from every served version
3. Include/exclude filters narrow down which CRDs are kept
4. Cross-source conflict detection catches the same group/kind/version with different content
5. Identical duplicates are deduplicated (first occurrence wins)
6. Schemas are written to `schemas/{group}/{apiVersion}/{kind}.json` with SHA-256 change detection
7. Provenance metadata (`.provenance.json`) and a CycloneDX SBOM (`sbom.cdx.json`) are generated alongside

Helm charts are scanned across all directories -- `crds/`, `templates/` (with Go template directive stripping), and any non-standard locations. This handles charts that place CRDs in different paths without requiring `helm template` at processing time.

## Adding a source

Create or edit a YAML file in `sources/` named after the primary API group (e.g., `cert-manager.io.yaml`):

```yaml
sources:
  - name: cert-manager
    type: helm
    repo: https://charts.jetstack.io
    chart: cert-manager
    version: v1.17.2
    license: Apache-2.0
    homepage: https://cert-manager.io
    values:                     # optional: helm --set key=value pairs
      crds.enabled: "true"
    include:                    # optional: allowlist (Kind, group/Kind, group/*)
      - "cert-manager.io/*"
    exclude:                    # optional: denylist (same syntax)
      - SomeKind
```

Source types:

| Type | Required fields | Description |
|------|----------------|-------------|
| `helm` | `repo`, `chart` | Helm chart from HTTP or OCI (`oci://` prefix) repository |
| `url` | `url` | Direct HTTP URL to a YAML manifest containing CRDs |

Source configs can be validated with the built-in command or against `source.schema.json`:

```bash
crd-schema-extractor validate sources/
check-jsonschema --schemafile source.schema.json sources/*.yaml
```

## Project structure

```
cmd/crd-schema-extractor/
  main.go                          CLI entrypoint (cobra root command, version subcommand)
  extract.go                       extract subcommand (parallel fetch + extract + write)
  validate.go                      validate subcommand (source config validation)
internal/
  source/source.go                Source config parsing (directory or single file)
  fetcher/
    fetcher.go                    Fetcher interface, Result type, factory
    helm_http.go                  Pure Go HTTP Helm repo fetcher (index.yaml + tarball)
    helm_oci.go                   OCI registry fetcher using oras-go
    url.go                        URL fetcher with retry
    untar.go                      Tarball extraction utilities
  extractor/
    extractor.go                  Extract() pipeline entry point, CRD parser
    process.go                    Chart scanning, template stripping dispatch
    strip.go                      Go template directive removal
    filter.go                     Include/exclude filtering
    conflict.go                   Cross-source conflict detection, dedup
  provenance/provenance.go        Per-schema provenance metadata
  sbom/sbom.go                    CycloneDX 1.5 SBOM generation
sources/                          Source config files ({api-group}.yaml)
schemas/                          Output directory
source.schema.json                JSON Schema for source configs (included in releases)
flake.nix                         Nix flake (build + dev shell)
.goreleaser.yaml                  Release configuration
```

## CI/CD

Three GitHub Actions workflows:

- **test.yml** (on PR): runs `go vet`, `go test -race`, and an E2E test that extracts schemas from cert-manager to verify the full pipeline
- **release-drafter.yml** (on push to main): auto-maintains a draft GitHub release with changelog from merged PRs
- **release.yaml** (on tag push): runs goreleaser to build binaries for linux/darwin (amd64/arm64), attach them to the release along with `source.schema.json` and checksums

Release flow: PRs merge to main, release-drafter updates the draft. When the draft is published, the tag is created, triggering goreleaser to build and attach artifacts.

## Version automation

[Renovate](https://docs.renovatebot.com/) is configured to open weekly PRs bumping Helm chart versions and GitHub release URL versions in the source configs.

## License

See individual source `license` fields for upstream CRD licensing. The extractor tool itself is available under the terms specified in the repository.
