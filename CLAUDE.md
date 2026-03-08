# CLAUDE.md

## Project overview

**crd-schema-extractor** is the standalone extractor component for the `crd-schemas` pipeline. It extracts JSON schemas from Kubernetes CustomResourceDefinitions (CRDs) published by upstream operators/controllers.

The extractor binary reads source config files, fetches CRDs from Helm charts or URLs, extracts the `openAPIV3Schema` from each served CRD version, and writes standalone JSON Schema files with provenance metadata and a CycloneDX SBOM.

GitHub repo: `blacksd/crd-schema-extractor`

## Repository structure

```
cmd/crd-schema-extractor/
  main.go                          CLI entrypoint (cobra root command, version subcommand)
  extract.go                       extract subcommand (parallel fetch + extract + write)
  validate.go                      validate subcommand (source config validation)
internal/
  source/source.go                Source config parsing (directory or single file via Load())
  fetcher/
    fetcher.go                    Fetcher interface, Result type, factory (New)
    helm_http.go                  Pure Go HTTP Helm repo fetcher (index.yaml + tarball)
    helm_oci.go                   OCI registry fetcher using oras-go
    url.go                        URL fetcher with retry
    untar.go                      Tarball extraction utilities
  extractor/
    extractor.go                  Extract() pipeline entry point, CRDSchema type, parseCRDs()
    process.go                    Chart scanning (crds/ + templates/ + recursive), template stripping dispatch
    strip.go                      Go template directive removal (StripTemplateDirectives)
    filter.go                     Include/exclude filtering (Filter, matchPattern)
    conflict.go                   Cross-source conflict detection (DetectConflicts), SchemaKey, dedup
  provenance/provenance.go        Per-schema provenance metadata generation
  sbom/sbom.go                    CycloneDX 1.5 SBOM generation (components sorted by name)
sources/                          Source config files ({api-group}.yaml)
schemas/                          Output directory
source.schema.json                JSON Schema for source configs (included in releases)
flake.nix                         Nix flake: buildGoModule + devShell (go, check-jsonschema, yq, goreleaser)
.goreleaser.yaml                  Release config (ldflags for version, append mode for release-drafter)
renovate.json                     Automated version bumps for Helm and GitHub-release URL sources
.github/
  release-drafter.yml             Release drafter config (categories, semver resolution)
  workflows/
    test.yml                      PR: go vet, go test -race, E2E against cert-manager
    release-drafter.yml           Push to main: auto-maintain draft release
    release.yaml                  Tag push: goreleaser builds + attaches artifacts
```

## Go module

- Module path: `github.com/blacksd/crd-schema-extractor`
- Go 1.25.7
- Key dependencies: `k8s.io/apiextensions-apiserver` (CRD types), `k8s.io/apimachinery` (YAML decoder), `github.com/CycloneDX/cyclonedx-go` (SBOM), `github.com/rs/zerolog` (logging), `sigs.k8s.io/yaml` (source config parsing), `github.com/spf13/cobra` (CLI), `oras.land/oras-go/v2` (OCI registry)
- No external runtime dependencies (no helm CLI required)

## How extraction works

1. `source.Load(path)` reads source configs from a directory or single file, groups by API group (derived from filename)
2. Sources are fetched in parallel (bounded by `--parallel`, default 4) via goroutines with a semaphore
3. For each source, `extractor.Extract()` calls `fetcher.New(src)` to get the appropriate fetcher:
   - **helm** HTTP: fetch `index.yaml`, resolve chart URL, download tarball, extract (pure Go)
   - **helm** OCI (`oci://` prefix): pull via oras-go, extract chart layer tarball (pure Go)
   - **url**: HTTP GET with retry (3 attempts, 2s interval)
4. `Process()` scans the fetched content for CRDs:
   - Helm charts: scan `crds/` (raw YAML), `templates/` (with Go template directive stripping), and recursive tree scan for non-standard locations
   - URL: pass raw data directly to parser
5. `parseCRDs()` decodes multi-document YAML, filters for `kind: CustomResourceDefinition`, unmarshals into `apiextensionsv1.CustomResourceDefinition`, extracts `openAPIV3Schema` from each served version
6. `Filter()` applies `include`/`exclude` patterns (Kind, group/Kind, group/*)
7. Results are collected in source-order after all goroutines complete
8. `DetectConflicts()` finds same group/kind/version with different schema content across sources (returns `map[string][]CRDSchema`)
9. Dedup identical schemas, write to `schemas/{group}/{apiVersion}/{kind}.json` with change detection (SHA-256)
10. Generate provenance `.provenance.json` per schema and `sbom.cdx.json` for all sources

## Source config format

```yaml
sources:
  - name: my-operator          # unique, lowercase, alphanumeric with hyphens
    type: helm                  # "helm" or "url"
    repo: https://charts.example.io   # helm HTTP repo
    # repo: oci://ghcr.io/org/charts  # helm OCI repo (oci:// prefix triggers direct pull)
    chart: my-operator                # helm chart name
    # url: https://example.com/crds.yaml  # url type: direct manifest URL
    version: v1.2.3
    license: Apache-2.0        # SPDX identifier
    homepage: https://example.io
    values:                     # optional: helm --set key=value pairs
      crds.install: "true"
    include:                    # optional: allowlist (Kind, group/Kind, group/*)
      - "mygroup.io/*"
    exclude:                    # optional: denylist (same syntax)
      - SomeKind
```

Validated against `source.schema.json`. One file per primary API group, filename = `{api-group}.yaml`.

## Build and test

```bash
# Dev shell (provides go, check-jsonschema, yq, goreleaser)
nix develop

# Run all tests
go test ./...

# Extract all sources
go run ./cmd/crd-schema-extractor/ extract sources/ -o schemas

# Extract a single source file with debug logging
go run ./cmd/crd-schema-extractor/ extract sources/cert-manager.io.yaml --debug

# Build binary via Nix
nix build
./result/bin/crd-schema-extractor version

# Build release binaries locally (snapshot, no tag required)
goreleaser build --snapshot --clean

# Validate source configs
crd-schema-extractor validate sources/
check-jsonschema --schemafile source.schema.json sources/*.yaml
```

## Release process

[release-drafter](https://github.com/release-drafter/release-drafter) auto-maintains a draft GitHub release as PRs merge to main. When the draft is published, the tag is created, triggering goreleaser to build binaries and attach them to the release.

Goreleaser uses `release.mode: append` to attach artifacts to the existing release without overwriting the release-drafter changelog. Version info is injected via ldflags (`main.version`, `main.commit`, `main.date`).

## Key design decisions

- **No helm CLI dependency**: all fetching is pure Go. HTTP repos use index.yaml parsing + tarball download. OCI registries use oras-go.
- **Template stripping instead of helm template**: CRD schemas are never templated -- only conditional wrappers and metadata labels use Go template directives. Stripping `{{ ... }}` patterns yields identical results without requiring `helm` at processing time.
- **Recursive chart scanning**: instead of only scanning `crds/`, the entire unpacked chart is scanned for YAML files containing CRD markers. Handles non-standard layouts (e.g., Kargo's `resources/crds/`).
- **One source file per API group**: filename is the API group name. Multiple sources in one file are allowed.
- **OCI support**: detected via `oci://` prefix on the `repo` field. Source type stays `helm` (no new type).
- **Version fallback**: tries version with `v` prefix stripped first, then retries with the original version string if that fails.
- **Parallel fetching**: sources are fetched concurrently (default 4), results collected in deterministic order. Disk writes, conflict detection, and dedup remain sequential.
- **Conflict detection**: same group/kind/apiVersion from different sources with different content is a fatal error. Identical duplicates are deduplicated. `DetectConflicts()` returns `map[string][]CRDSchema` directly (no separate Conflict type).
- **Change detection**: schemas are only rewritten if SHA-256 differs, keeping git history clean.
- **SBOM components sorted by name** for stable diffs.

## CI workflows

- **test.yml** (on PR): `go vet`, `go test -race`, E2E test extracting schemas from cert-manager
- **release-drafter.yml** (on push to main): auto-maintains draft release with changelog from merged PRs
- **release.yaml** (on tag push): goreleaser builds binaries for linux/darwin (amd64/arm64), attaches to release with `source.schema.json` and checksums

## Renovate automation

Two custom regex managers in `renovate.json`:
1. Helm chart versions: matches `type: helm` + `repo:` + `chart:` + `version:` pattern, uses `helm` datasource
2. GitHub release URLs: matches `type: url` + GitHub release download URL pattern, uses `github-releases` datasource

Weekly PRs (Monday before 6am), no automerge.
