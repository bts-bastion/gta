# Go Workspace Support for GTA

## Investigation and Implementation Plan

**Date**: 2026-03-26
**Status**: Proposal
**Upstream repo**: [digitalocean/gta](https://github.com/digitalocean/gta)
**Fork**: bts-bastion/gta

---

## Table of Contents

1. [Executive Summary](#1-executive-summary)
2. [Background](#2-background)
3. [Current Architecture Analysis](#3-current-architecture-analysis)
4. [Go Workspace Mechanics](#4-go-workspace-mechanics)
5. [Upstream Issues and Community Discussion](#5-upstream-issues-and-community-discussion)
6. [Prior Art: Similar Tools](#6-prior-art-similar-tools)
7. [Gap Analysis](#7-gap-analysis)
8. [Implementation Plan](#8-implementation-plan)
9. [Testing Strategy](#9-testing-strategy)
10. [Risks and Mitigations](#10-risks-and-mitigations)
11. [References](#11-references)

---

## 1. Executive Summary

GTA (Go Target Analysis) finds Go packages that have changed relative to a git branch and computes their transitive dependents. It is designed for monorepo CI pipelines to test only affected packages. However, GTA currently operates in **single-module mode only** and has no support for [Go workspaces](https://go.dev/ref/mod#workspaces) (`go.work` files), introduced in Go 1.18.

This document proposes modifications to enable full Go workspace support. The changes are relatively contained because:

- `golang.org/x/tools/go/packages` (already used by GTA) is **transparently workspace-aware** -- it correctly loads packages across all workspace modules with no code changes.
- The `golang.org/x/mod/modfile` package (already vendored) includes `ParseWork` for parsing `go.work` files.
- The `GTA.roots` field is already a `[]string`, so the data model supports multiple module roots.

The primary changes are in `toplevel()` (detect workspace mode, return multiple roots) and minor adjustments to `isIgnoredByGo()` root handling.

---

## 2. Background

### What is GTA?

GTA determines which Go packages have changed between a git branch and its base, then computes all transitive dependents. In a monorepo, this means CI only tests the packages actually affected by a PR.

**Core flow**:
1. **Differ** (`differ.go`): Uses `git diff` to find changed files
2. **Packager** (`packager.go`): Loads all Go packages via `packages.Load`, builds forward/reverse dependency graphs
3. **GTA** (`gta.go`): Maps changed files to packages, traverses the reverse dependency graph to find all affected packages

### What are Go Workspaces?

Go workspaces ([spec](https://go.dev/ref/mod#workspaces)) allow multiple Go modules to coexist in a single repository and be developed together. A `go.work` file at the repo root declares which modules are part of the workspace:

```
go 1.25

use (
    ./services/api
    ./services/worker
    ./libs/shared
)
```

When `go.work` is present, Go commands automatically resolve cross-module imports to local directories rather than fetching them from the module proxy. This is the idiomatic way to manage multi-module monorepos.

---

## 3. Current Architecture Analysis

### 3.1 `toplevel()` -- Single Module Root Assumption

**File**: `gta.go:602-613`

```go
func toplevel() ([]string, error) {
    if os.Getenv("GO111MODULE") == "off" {
        return gopaths()
    }
    root, err := moduleroot()
    if err != nil {
        return nil, err
    }
    return []string{root}, nil
}
```

**`moduleroot()`** at `gta.go:629-637`:

```go
func moduleroot() (string, error) {
    cmd := exec.Command("go", "list", "-m", "-f", "{{.Dir}}")
    b, err := cmd.CombinedOutput()
    if err != nil {
        return "", fmt.Errorf("could get not get module root: %w", err)
    }
    return strings.TrimSpace(string(b)), nil
}
```

**Problem**: `go list -m -f '{{.Dir}}'` returns **multiple lines** in workspace mode (one per workspace module), but `strings.TrimSpace` treats the entire output as a single value. This causes GTA to either use only the first module or produce an incorrect concatenated path.

### 3.2 `dependencyGraph()` -- Already Workspace-Compatible

**File**: `packager.go:259-391`

The function calls `packages.Load(cfg, patterns...)` with a `"..."` pattern when no prefixes are set. In workspace mode, `packages.Load` transparently loads packages from **all** workspace modules because it delegates to `go list`, which is workspace-aware.

The `moduleNamesByDir` map at line 302-303:

```go
if pkg.Module != nil && pkg.Module.Main {
    moduleNamesByDir[pkg.Module.Dir] = pkg.Module.Path
}
```

This already handles multiple main modules correctly -- each workspace module with `Main == true` gets its own entry.

### 3.3 `resolveLocal()` -- Already Multi-Module Compatible

**File**: `packager.go:215-253`

This function resolves `"."` import paths by finding the longest matching prefix in `modulesByDir`. Since `moduleNamesByDir` will contain entries for all workspace modules (populated by `packages.Load`), `resolveLocal()` will correctly resolve packages in any workspace module.

### 3.4 Git Differ -- Already Repository-Scoped

**File**: `differ.go:170-174`

```go
out, err := execWithStderr(exec.Command("git", "rev-parse", "--show-toplevel"))
root := strings.TrimSpace(string(out))
```

The differ uses `git rev-parse --show-toplevel` to get the repository root, which encompasses all workspace modules. Changed files from any module in the workspace will be detected. No changes needed.

### 3.5 `isIgnoredByGo()` -- Uses `roots` for Exception Handling

**File**: `gta.go:536-561`

This function checks whether a directory is ignored by Go (e.g., `testdata`, `.hidden`, `_private`). The `roots` parameter allows certain directories to be exempted. Currently, `roots` contains a single module root. With workspaces, it should contain all workspace module roots so that none are accidentally ignored if they happen to start with `_` or `.`.

### 3.6 The `GTA` Struct -- Already Supports Multiple Roots

**File**: `gta.go:89-96`

```go
type GTA struct {
    differ                    Differ
    packager                  Packager
    prefixes                  []string
    tags                      []string
    roots                     []string  // <-- already a slice
    includeTransitiveTestDeps bool
}
```

The `roots` field is `[]string`, set by `toplevel()` in `New()`. The data model already supports multiple roots.

---

## 4. Go Workspace Mechanics

### 4.1 `go.work` File Structure

A `go.work` file supports four directives:

| Directive | Purpose |
|-----------|---------|
| `go 1.25` | Go version (required) |
| `use ./path` | Add a module directory to the workspace |
| `replace old => new` | Workspace-level dependency replacement |
| `toolchain go1.25.0` | Toolchain version (optional) |

Each `use` directive points to a directory containing a `go.mod` file. Paths are relative to the `go.work` file.

**Source**: [Go Modules Reference: Workspaces](https://go.dev/ref/mod#workspaces)

### 4.2 Workspace Mode Detection

Workspace mode is active when:
1. A `go.work` file exists in the current directory or any ancestor, **and**
2. `GOWORK` is not set to `"off"`

Detection method: `go env GOWORK` returns the path to the active `go.work` file, or empty if not in workspace mode.

**Source**: [Go Modules Reference: GOWORK](https://go.dev/ref/mod#gowork)

### 4.3 `packages.Load` Behavior in Workspace Mode

The [workspace proposal](https://go.googlesource.com/proposal/+/master/design/45713-workspace.md) explicitly states:

> "Tools based on the go command, either directly through `go list` or via `golang.org/x/tools/go/packages` will work without changes with workspaces."

Key behaviors:
- `packages.Load(cfg, "...")` loads packages from **all** workspace modules
- Each returned `packages.Package` has a `Module` field with `Module.Main == true` for workspace modules
- Cross-module imports are resolved to local workspace directories
- `Config.Dir` determines where Go looks for `go.work`
- `Config.Env` can include `GOWORK=off` to disable workspace mode

**Source**: [golang.org/x/tools/go/packages](https://pkg.go.dev/golang.org/x/tools/go/packages)

### 4.4 `go list -m` in Workspace Mode

In single-module mode, `go list -m -f '{{.Dir}}'` returns one directory. In workspace mode, it returns **multiple directories** (one per workspace module). Example:

```
$ go list -m -f '{{.Dir}}'
/repo/services/api
/repo/services/worker
/repo/libs/shared
```

**Source**: [Issue #52649](https://github.com/golang/go/issues/52649)

### 4.5 Vendoring and Workspaces

| Go Version | Workspace Vendoring |
|------------|-------------------|
| 1.18-1.21 | **Not supported**. Module vendor directories ignored in workspace mode. |
| 1.22+ | `go work vendor` creates a workspace-level vendor directory. `go mod vendor` **fails** when `go.work` exists. |

**Source**: [Issue #60056](https://github.com/golang/go/issues/60056), [Go 1.22 Release Notes](https://go.dev/doc/go1.22)

### 4.6 The `./...` Pattern vs `...` Pattern

- `./...` matches only packages under the current directory
- `...` (without `./`) in workspace mode matches packages across **all** workspace modules
- There is no `./...` equivalent for "all packages in all workspace modules" -- tools must use bare `...` or enumerate module directories

**Source**: [Issue #50745](https://github.com/golang/go/issues/50745)

---

## 5. Upstream Issues and Community Discussion

### 5.1 Issue #24: "Error listing changed packages in a nested module" (OPEN)

**URL**: https://github.com/digitalocean/gta/issues/24
**Filed**: September 2021 by `@jeromefroe`

This is the **canonical issue** for workspace support. The reporter has a monorepo with nested modules and gets `"<package> not found"` errors because `packages.Load` only sees the top-level module's packages.

Maintainer `@bhcleek` confirmed the root cause and commented:

> "It might be worth it for us to wait for the go.work proposal to be implemented, because trying to use dependencyGraph as-is is going to require changing the working directory when packages.Load is run to be within each module."

**Status**: Open since 2021, no implementation activity since Go 1.18 shipped workspaces.

### 5.2 Issue #58: "Support repositories that do not have dependencies vendored" (OPEN)

**URL**: https://github.com/digitalocean/gta/issues/58
**Filed**: April 2025 by `@ndombroski`

Related to workspace support because workspace vendoring (via `go work vendor`) has different semantics than per-module vendoring. The maintainer provided detailed background on why vendoring is currently assumed and potential paths forward.

### 5.3 Issue #27: "updates to go.mod needed" (CLOSED)

**URL**: https://github.com/digitalocean/gta/issues/27
**Filed**: 2021 by `@alexbilbie`

Extensive discussion (33 comments) about monorepo Lambda apps and vendoring. Reveals deep concerns about dependency comparison across git refs.

### 5.4 Issue #50: "Support targeting a specific package" (OPEN)

**URL**: https://github.com/digitalocean/gta/issues/50

Requests a `-check` flag for targeting specific packages in monorepos -- relevant to workspace use cases.

### 5.5 PR #11: "Module support take 2" (MERGED)

**URL**: https://github.com/digitalocean/gta/pull/11
**Merged**: December 2020

The foundational PR that introduced `golang.org/x/tools/go/packages` and `dependencyGraph()`. Built entirely around single-module semantics.

### 5.6 Summary of Upstream Position

- The maintainer acknowledged workspace support as the right approach (2021)
- Go 1.18 shipped workspaces in March 2022
- Over 4 years later, **no implementation work has started upstream**
- Any workspace support in our fork would be novel work

---

## 6. Prior Art: Similar Tools

### 6.1 Knit (github.com/nicolasgere/knit)

The most directly comparable tool: a "zero-config tool for Go workspace monorepos."

**Approach**:
1. Runs `go list -m -json` from workspace root to enumerate all modules
2. Builds package patterns per module (e.g., `./services/api/...`)
3. Uses `go list -json <patterns>` from workspace root to load all packages
4. Maps changed files (from `git diff`) to modules by checking directory prefixes
5. Builds a module-level dependency DAG and traverses it to find affected modules

**Key difference from GTA**: Knit operates at module granularity (coarser), while GTA operates at package granularity (finer, more precise).

**Source**: https://github.com/nicolasgere/knit

### 6.2 Bazel/Gazelle

Does not use `go.work`. Gazelle generates BUILD files from `go.mod` and uses Bazel's own dependency graph. Multi-module monorepos require combining all `go.mod` files into a single root.

**Source**: https://github.com/bazelbuild/bazel-gazelle

### 6.3 Key Patterns from the Ecosystem

**Detecting workspace mode**:
```go
cmd := exec.Command("go", "env", "GOWORK")
output, _ := cmd.Output()
gowork := strings.TrimSpace(string(output))
// gowork is path to go.work file, or "" if not in workspace mode
```

**Parsing go.work to enumerate modules**:
```go
data, _ := os.ReadFile(goworkPath)
workFile, _ := modfile.ParseWork("go.work", data, nil)
for _, use := range workFile.Use {
    moduleDir := filepath.Join(filepath.Dir(goworkPath), use.Path)
    // moduleDir is absolute path to module root
}
```

**Source**: [golang.org/x/mod/modfile](https://pkg.go.dev/golang.org/x/mod/modfile), already vendored at `vendor/golang.org/x/mod/modfile/work.go`

---

## 7. Gap Analysis

| Component | Current Behavior | Workspace Requirement | Change Needed |
|-----------|-----------------|----------------------|---------------|
| `toplevel()` | Returns single module root via `go list -m -f '{{.Dir}}'` | Must return all workspace module roots | **Yes** -- detect workspace, parse `go.work` or split multi-line output |
| `moduleroot()` | Calls `go list -m -f '{{.Dir}}'`, expects one line | Must handle multi-line output | **Yes** -- return `[]string` or replace with workspace-aware logic |
| `dependencyGraph()` | Calls `packages.Load(cfg, "...")` | Same call works in workspace mode | **No** -- already workspace-compatible |
| `moduleNamesByDir` | Populated from `pkg.Module` | Multiple `Main == true` modules | **No** -- already handles multiple main modules |
| `resolveLocal()` | Iterates `modulesByDir` for longest prefix | Works with multiple modules | **No** -- already multi-module compatible |
| `isIgnoredByGo()` | Uses `roots` for exceptions | Needs all workspace module roots | **No** -- already works with `[]string` roots |
| Git differ | Uses `git rev-parse --show-toplevel` | Repo root encompasses all modules | **No** -- already correct |
| `newLoadConfig()` | Configures `packages.Config` | May want to set `GOWORK` explicitly | **Optional** -- consider allowing explicit `GOWORK` control |
| `GTA.roots` | Set from `toplevel()` | Already `[]string` | **No** -- data model is ready |
| `cmd/gta/main.go` | No workspace flags | May want `-workspace=off` flag | **Optional** -- useful for users who want to opt out |

**Summary**: Only `toplevel()`/`moduleroot()` require mandatory changes. Everything else is either already compatible or optional enhancement.

---

## 8. Implementation Plan

### Phase 1: Core Workspace Detection (Required)

#### 8.1 Add `workspaceroots()` function

**File**: `gta.go`

Add a new function that detects workspace mode and returns all module roots:

```go
func workspaceroots() ([]string, error) {
    // Check if we're in workspace mode
    cmd := exec.Command("go", "env", "GOWORK")
    b, err := cmd.CombinedOutput()
    if err != nil {
        return nil, fmt.Errorf("could not get GOWORK: %w", err)
    }
    gowork := strings.TrimSpace(string(b))
    if gowork == "" || gowork == "off" {
        return nil, nil // not in workspace mode
    }

    // Parse the go.work file to get module directories
    data, err := os.ReadFile(gowork)
    if err != nil {
        return nil, fmt.Errorf("could not read go.work file: %w", err)
    }

    workFile, err := modfile.ParseWork(gowork, data, nil)
    if err != nil {
        return nil, fmt.Errorf("could not parse go.work file: %w", err)
    }

    workDir := filepath.Dir(gowork)
    var roots []string
    for _, use := range workFile.Use {
        absDir := filepath.Join(workDir, use.Path)
        roots = append(roots, absDir)
    }

    return roots, nil
}
```

**Rationale**: Using `modfile.ParseWork` (already vendored) is more reliable than parsing `go list -m` output, and gives us access to the workspace structure. It also avoids the `go list -m` multi-line parsing issue entirely.

#### 8.2 Modify `toplevel()` to check for workspaces first

**File**: `gta.go:602-613`

```go
func toplevel() ([]string, error) {
    if os.Getenv("GO111MODULE") == "off" {
        return gopaths()
    }

    // Check for workspace mode first
    roots, err := workspaceroots()
    if err != nil {
        return nil, err
    }
    if roots != nil {
        return roots, nil
    }

    // Fall back to single module mode
    root, err := moduleroot()
    if err != nil {
        return nil, err
    }
    return []string{root}, nil
}
```

#### 8.3 Add `modfile` import

The `modfile` package is already vendored at `vendor/golang.org/x/mod/modfile/work.go`. Add the import to `gta.go`:

```go
import (
    // ... existing imports ...
    "golang.org/x/mod/modfile"
)
```

### Phase 2: Options and CLI (Recommended)

#### 8.4 Add `SetWorkspace` option

**File**: `options.go`

```go
// SetWorkspace controls workspace mode. When set to false, workspace mode
// is disabled even if a go.work file exists (equivalent to GOWORK=off).
func SetWorkspace(enabled bool) Option {
    return func(g *GTA) error {
        if !enabled {
            g.disableWorkspace = true
        }
        return nil
    }
}
```

Add `disableWorkspace bool` field to the `GTA` struct, and check it in `toplevel()` (or pass `GOWORK=off` via the packages config env).

#### 8.5 Add `-workspace` CLI flag

**File**: `cmd/gta/main.go`

```go
flagWorkspace := flag.Bool("workspace", true, "enable Go workspace (go.work) support")
```

When set to `false`, pass `SetWorkspace(false)` to disable workspace detection.

### Phase 3: Workspace-Aware Package Loading (Optional Enhancement)

#### 8.6 Pass `GOWORK` to `packages.Config`

**File**: `packager.go:82-98`

When workspace is disabled, set `GOWORK=off` in the packages config environment:

```go
func newLoadConfig(tags []string) *packages.Config {
    cfg := &packages.Config{
        Mode: packages.NeedName |
            packages.NeedFiles |
            packages.NeedEmbedFiles |
            packages.NeedImports |
            packages.NeedDeps |
            packages.NeedModule |
            packages.NeedForTest,
        BuildFlags: []string{
            fmt.Sprintf(`-tags=%s`, strings.Join(tags, ",")),
        },
        Tests: true,
    }
    return cfg
}
```

To disable workspace mode, add `GOWORK=off` to `cfg.Env`. This would require threading the workspace flag through to the config constructor.

### Phase 4: go.work Change Detection (Enhancement)

#### 8.7 Handle changes to `go.work` itself

**File**: `gta.go`, in `markedPackages()`

The existing TODO at line 261 notes:

```go
// TODO(bc): handle changes to go.mod when vendoring is not being used.
```

Similarly, when `go.work` changes (e.g., a new `use` directive is added or removed), all packages across all workspace modules should be considered changed. This can be detected by checking if the differ reports changes to the `go.work` file.

```go
// In markedPackages(), after getting dirs from differ:
for abs := range dirs {
    if filepath.Base(abs) == "go.work" {
        // go.work changed; mark all packages as changed
        // ... implementation ...
    }
}
```

---

## 9. Testing Strategy

### 9.1 Unit Tests

Add test cases to `gta_test.go` using the existing `testDiffer`/`testPackager` pattern:

1. **Workspace with multiple modules**: Changed file in module A affects dependents in module B
2. **Workspace with no changes**: No changed packages when workspace modules haven't changed
3. **Workspace with deleted module**: Module directory removed from workspace
4. **go.work change detection**: All packages marked when go.work is modified

### 9.2 Integration Tests

Extend `gtaintegration/gtaintegration_test.go`:

1. **Create a workspace repo**: Multiple modules with cross-module dependencies
2. **Test cross-module dependency detection**: Change a package in one module, verify dependents in another module are found
3. **Test workspace disable**: Verify `-workspace=false` falls back to single-module behavior
4. **Test vendor interaction**: Verify behavior with `go work vendor` (Go 1.22+)

### 9.3 Test Data

Create a new testdata fixture with workspace structure:

```
testdata/workspace/
    go.work
    moduleA/
        go.mod
        pkg/
            a.go      # imports moduleB/pkg
    moduleB/
        go.mod
        pkg/
            b.go      # standalone package
    moduleC/
        go.mod
        pkg/
            c.go      # imports moduleA/pkg (transitive dependency on moduleB)
```

---

## 10. Risks and Mitigations

### 10.1 Backward Compatibility

**Risk**: Existing users in single-module repos might be affected.
**Mitigation**: Workspace detection is additive. `toplevel()` falls back to `moduleroot()` when not in workspace mode. The `roots` field is already a slice. No existing behavior changes.

### 10.2 Performance

**Risk**: Loading packages across many workspace modules could be slower.
**Mitigation**: `packages.Load(cfg, "...")` already loads all packages when no prefix filter is set. In workspace mode it loads more packages, but this is inherent to the workspace size, not a regression. The `-include` prefix flag already exists for filtering.

### 10.3 Vendoring Compatibility

**Risk**: Workspace vendoring (`go work vendor`) has different semantics than per-module vendoring.
**Mitigation**: Since `packages.Load` handles vendoring transparently, no special handling should be needed. Document the requirement for Go 1.22+ when using workspace vendoring.

### 10.4 `go.work` Not at Git Root

**Risk**: The `go.work` file might not be at the git repository root (e.g., nested workspace).
**Mitigation**: `go env GOWORK` returns the correct `go.work` path regardless of the current directory. The differ already uses `git rev-parse --show-toplevel` for the git root, and changed file paths are absolute, so they will correctly match against module directories.

### 10.5 Toolchain Version Conflicts

**Risk**: `packages.Load` can fail when `go.work` and `go.mod` files have conflicting `toolchain` directives.
**Mitigation**: Document this as a known limitation. See [Go issue #69646](https://github.com/golang/go/issues/69646).

---

## 11. References

### Go Specification and Documentation

| Resource | URL |
|----------|-----|
| Go Modules Reference: Workspaces | https://go.dev/ref/mod#workspaces |
| Go Blog: Get Familiar with Workspaces | https://go.dev/blog/get-familiar-with-workspaces |
| Go Tutorial: Multi-Module Workspaces | https://go.dev/doc/tutorial/workspaces |
| Workspace Mode Proposal (Design Doc) | https://go.googlesource.com/proposal/+/master/design/45713-workspace.md |
| Go 1.22 Release Notes (workspace vendoring) | https://go.dev/doc/go1.22 |

### Go Issues

| Issue | Description | URL |
|-------|-------------|-----|
| #45713 | Workspace mode proposal | https://github.com/golang/go/issues/45713 |
| #50745 | Iterate all packages in workspace | https://github.com/golang/go/issues/50745 |
| #52649 | `go list -m` docs for workspace mode | https://github.com/golang/go/issues/52649 |
| #60056 | Workspace vendoring support | https://github.com/golang/go/issues/60056 |
| #51919 | `go list -m -json all` hangs with go.work | https://github.com/golang/go/issues/51919 |
| #69646 | `packages.Load` fails with toolchain conflicts | https://github.com/golang/go/issues/69646 |

### Upstream GTA Issues

| Issue | Description | URL |
|-------|-------------|-----|
| #24 | Error listing changed packages in nested module | https://github.com/digitalocean/gta/issues/24 |
| #58 | Support repos without vendored dependencies | https://github.com/digitalocean/gta/issues/58 |
| #27 | Updates to go.mod needed | https://github.com/digitalocean/gta/issues/27 |
| #50 | Support targeting a specific package | https://github.com/digitalocean/gta/issues/50 |
| PR #11 | Module support (foundational PR) | https://github.com/digitalocean/gta/pull/11 |

### API Documentation

| Package | URL |
|---------|-----|
| `golang.org/x/tools/go/packages` | https://pkg.go.dev/golang.org/x/tools/go/packages |
| `golang.org/x/mod/modfile` (WorkFile, ParseWork) | https://pkg.go.dev/golang.org/x/mod/modfile |

### Similar Tools

| Tool | Description | URL |
|------|-------------|-----|
| Knit | Zero-config Go workspace monorepo tool | https://github.com/nicolasgere/knit |
| Gazelle | Bazel BUILD file generator for Go | https://github.com/bazelbuild/bazel-gazelle |
| flowerinthenight/golang-monorepo | Example Go monorepo with selective builds | https://github.com/flowerinthenight/golang-monorepo |

### Articles and Guides

| Resource | URL |
|----------|-----|
| Earthly: Go Workspaces | https://earthly.dev/blog/go-workspaces/ |
| Gopls Workspace Documentation | https://github.com/golang/tools/blob/master/gopls/doc/workspace.md |

### Source Code References (This Repo)

| File | Lines | Relevance |
|------|-------|-----------|
| `gta.go` | 602-637 | `toplevel()` and `moduleroot()` -- primary change targets |
| `gta.go` | 89-96 | `GTA` struct with `roots []string` field |
| `gta.go` | 536-561 | `isIgnoredByGo()` -- uses roots for exceptions |
| `gta.go` | 239-453 | `markedPackages()` -- maps changed files to packages |
| `packager.go` | 82-98 | `newLoadConfig()` -- packages.Config setup |
| `packager.go` | 259-391 | `dependencyGraph()` -- package loading, already workspace-compatible |
| `packager.go` | 215-253 | `resolveLocal()` -- import path resolution, already multi-module compatible |
| `packager.go` | 300-303 | `moduleNamesByDir` population -- already handles multiple main modules |
| `differ.go` | 166-237 | Git differ -- repository-scoped, no changes needed |
| `options.go` | 1-53 | Option functions -- add `SetWorkspace` here |
| `cmd/gta/main.go` | 28-103 | CLI entry point -- add `-workspace` flag here |
| `vendor/golang.org/x/mod/modfile/work.go` | 1-334 | `ParseWork`, `WorkFile`, `Use` types -- already vendored |
