# CLAUDE.md

## Project overview

**crd-schema-extractor** is the standalone extractor component for the `crd-schemas` pipeline. It extracts JSON schemas from Kubernetes CustomResourceDefinitions (CRDs) published by upstream operators/controllers.

The extractor binary reads source config files, fetches CRDs from Helm charts or URLs, extracts the `openAPIV3Schema` from each served CRD version, and writes standalone JSON Schema files with provenance metadata and a CycloneDX SBOM.

GitHub repo: `blacksd/crd-schema-extractor`

## Repository structure

```
cmd/extract/main.go          # CLI entrypoint: orchestrates load → extract → filter → conflict-check → dedup → write
internal/
  source/source.go            # Source config parsing (YAML files in sources/)
  extractor/extractor.go      # Core extraction: Helm (HTTP + OCI) and URL fetching, CRD parsing, filtering, conflict detection
  provenance/provenance.go    # Per-schema provenance metadata generation
  sbom/sbom.go                # CycloneDX 1.5 SBOM generation (components sorted by name)
sources/                      # One YAML config file per API group (filename = {api-group}.yaml)
schemas/                      # Output: {group}/{apiVersion}/{kind}.json + .provenance.json + sbom.cdx.json
source.schema.json            # JSON Schema for validating source config files
flake.nix                     # Nix flake: buildGoModule for the binary, devShell with go/helm/check-jsonschema/yq
renovate.json                 # Automated version bumps for Helm and GitHub-release URL sources
.github/workflows/
  update-schemas.yml          # On push to main: build, extract, validate, commit schemas, create version tags
  validate.yml                # On PR: validate source configs against source.schema.json
```

## Go module

- Module path: `github.com/blacksd/crd-schema-extractor`
- Go 1.25.7
- Key dependencies: `k8s.io/apiextensions-apiserver` (CRD types), `k8s.io/apimachinery` (YAML decoder), `github.com/CycloneDX/cyclonedx-go` (SBOM), `github.com/rs/zerolog` (logging), `sigs.k8s.io/yaml` (source config parsing)

## How extraction works

1. `source.LoadAll(dir)` reads `sources/*.yaml`, groups by API group (derived from filename)
2. For each source, `extractor.Extract()` dispatches by type:
   - **helm** (HTTP repo): `helm repo add` → `helm repo update` → `helm pull --untar` → scan `crds/` dir + `helm template --include-crds --no-hooks`
   - **helm** (OCI repo, `oci://` prefix): `helm pull oci://... --untar` directly (no repo add/update)
   - **url**: HTTP GET with retry (3 attempts, 2s interval)
3. `parseCRDs()` decodes multi-document YAML, filters for `kind: CustomResourceDefinition`, unmarshals into `apiextensionsv1.CustomResourceDefinition`, extracts `openAPIV3Schema` from each served version
4. `Filter()` applies `include`/`exclude` patterns (Kind, group/Kind, group/*)
5. `DetectConflicts()` finds same group/kind/version with different schema content across sources
6. Dedup identical schemas, write to `schemas/{group}/{apiVersion}/{kind}.json` with change detection (SHA-256)
7. Generate provenance `.provenance.json` per schema and `sbom.cdx.json` for all sources

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
# Dev shell (provides go, helm, check-jsonschema, yq, goreleaser)
nix develop

# Run all tests
go test ./...

# Extract all sources
go run ./cmd/extract/ -sources sources -output schemas

# Extract a single source (by name field, not filename)
go run ./cmd/extract/ -source cert-manager -debug

# Build binary via Nix
nix build
./result/bin/extract --sources=sources --output=schemas

# Build release binaries locally (snapshot, no tag required)
goreleaser build --snapshot --clean

# Validate source configs
check-jsonschema --schemafile source.schema.json sources/*.yaml
```

## Release process

Releases are built by goreleaser, triggered by pushing a semver tag:

```bash
git tag v0.1.0
git push origin v0.1.0
```

The `.github/workflows/release.yaml` workflow runs goreleaser to build binaries for linux/darwin (amd64/arm64), create archives, checksums, and a GitHub release.

## Key design decisions

- **One source file per API group**: filename is the API group name. Multiple sources in one file are allowed (used when an API group has CRDs across multiple individual files, e.g., Kubeflow, Crunchy PGO).
- **OCI support**: detected via `oci://` prefix on the `repo` field. Source type stays `helm` (no new type). Skips `repo add`/`repo update`, pulls directly.
- **Version fallback**: tries `helm pull --version X` with the `v` prefix stripped first, then retries with the original version string if that fails.
- **Helm template + crds/ scanning**: both paths are tried. Some charts put CRDs in `crds/` (standard Helm), others render them via templates (Kargo, Envoy Gateway). Charts that gate CRDs behind values need `values:` in the source config.
- **Conflict detection**: same group/kind/apiVersion from different sources with different content is a fatal error. Identical duplicates are logged as info and deduplicated.
- **SBOM components sorted by name** for stable diffs.
- **Change detection**: schemas are only rewritten if SHA-256 differs, keeping git history clean.

## Current state (as of 2026-03-07)

- 128 sources, 832 schemas, 0 conflicts
- Covers major operators: cert-manager, Argo CD/Rollouts/Workflows/Events, Prometheus, Istio, Calico, Traefik, Linkerd, Rook-Ceph, Longhorn, CloudNativePG, Strimzi, Flux, Kyverno, KEDA, Velero, Sealed Secrets, Grafana Operator, OpenTelemetry, and many more
- OCI sources: ARC v2 (actions.github.com), Envoy Gateway, Kargo
- URL-from-GitHub sources: Dragonfly, Kubeflow Training, Crunchy PGO (bypassing auth-gated OCI registries)

## Known gaps (from CONTRIBUTING.md)

Projects NOT covered due to technical limitations:
- **Cilium**: CRDs embedded in agent binary, not in Helm chart
- **Crossplane**: CRDs dynamically generated at runtime
- **Karpenter**: provider-specific OCI registries require auth
- **CSI Volume Snapshots**: multiple individual files, no combined manifest
- **EMQX**: no license declared
- **MySQL Oracle**: proprietary licensing
- **Flink**: version-coupled Helm repo URL breaks automation

## Renovate automation

Two custom regex managers in `renovate.json`:
1. Helm chart versions: matches `type: helm` + `repo:` + `chart:` + `version:` pattern, uses `helm` datasource
2. GitHub release URLs: matches `type: url` + GitHub release download URL pattern, uses `github-releases` datasource

Weekly PRs (Monday before 6am), no automerge.

## CI workflows

- **update-schemas.yml** (push to main): Nix build → extract all → validate JSON schemas → commit schemas → create version tags per changed source
- **validate.yml** (PR): validate source configs against `source.schema.json` via `check-jsonschema`
