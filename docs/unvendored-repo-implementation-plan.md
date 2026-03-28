# Unvendored Repository Support: Detailed Implementation Plan

**Date**: 2026-03-27
**Source**: `docs/unvendored-repo-support-plan.md`
**Branch**: `bts/go-unvendored`
**Status**: Ready for implementation

---

## Table of Contents

1. [Overview](#1-overview)
2. [Key Design Decision: Interface Compatibility](#2-key-design-decision-interface-compatibility)
3. [Commit Plan Summary](#3-commit-plan-summary)
4. [Commit 1: Add isLocalPackage and LocalImportersOf to packageContext](#4-commit-1)
5. [Commit 2: Skip Packages with Load Errors in Graph Construction](#5-commit-2)
6. [Commit 3: Tolerate PackageFromImport Errors for External Packages](#6-commit-3)
7. [Commit 4: Filter Non-Local Dependents from Graph Traversal](#7-commit-4)
8. [Commit 5: Add go.mod and go.sum Diff Functions](#8-commit-5)
9. [Commit 6: Add BaseFileReader Interface and Options](#9-commit-6)
10. [Commit 7: Replace Mark-All with Precise Dependency Diff](#10-commit-7)
11. [Test Fixtures](#11-test-fixtures)
12. [Validation Gates](#12-validation-gates)
13. [Regression Checklist](#13-regression-checklist)
14. [Rollback Strategy](#14-rollback-strategy)
15. [Commit Dependency Graph](#15-commit-dependency-graph)

---

## 1. Overview

This plan implements unvendored repository support for GTA in 7 atomic commits. Each commit:

- Leaves `go test ./...` green
- Contains both code and tests
- Is independently reviewable
- Follows the ordering: **pure additions -> defensive fixes -> new capabilities -> integration**

The work maps to the design document's phases:

| Design Phase | Commits | Goal |
|---|---|---|
| Phase 1: Graceful External Package Handling | 1, 2, 3, 4 | GTA doesn't crash on unvendored repos |
| Phase 2: Dependency Diff Analysis | 5, 6, 7 | Precise detection via go.mod/go.sum diffs |
| Phase 3: Workspace Support | 7 (partial) | Per-module go.mod/go.sum handling |

---

## 2. Key Design Decision: Interface Compatibility

**`LocalImportersOf` must NOT be added to the `Packager` interface.**

`Packager` is a public exported interface (`packager.go:42`). Adding a method would break any external implementation. Instead:

- Add `isLocalPackage()` and `LocalImportersOf()` as methods on `*packageContext` only (the concrete type)
- Use a small unexported interface + type assertion at the call site in `gta.go`:

```go
type localImporterFinder interface {
    LocalImportersOf(modulePaths []string) []string
}

if finder, ok := g.packager.(localImporterFinder); ok {
    // Use precise marking
} else {
    // Fall back to "mark all" for custom Packager implementations
}
```

This preserves full backward compatibility while giving precise behavior when using the default `packageContext`.

Similarly, `BaseFileReader` is a separate interface from `Differ`:

```go
type BaseFileReader interface {
    ReadBaseFile(relativePath string) ([]byte, error)
}
```

The git differ implements it; the file differ does not. Call sites use type assertions.

---

## 3. Commit Plan Summary

| # | Commit | Files | Risk | Dependencies |
|---|---|---|---|---|
| 1 | `isLocalPackage` + `LocalImportersOf` on `packageContext` | `packager.go`, `packager_test.go` | None | - |
| 2 | Skip `pkg.Errors` in `addPackage` | `packager.go`, `packager_test.go` | Low | - |
| 3 | Tolerate `PackageFromImport` errors | `gta.go`, tests | Medium | - |
| 4 | Filter non-local dependents in traversal | `gta.go`, tests | Low | Commit 1 |
| 5 | `gomod.go`: `diffGoMod` + `diffGoSum` | `gomod.go` (new), `gomod_test.go` (new) | None | - |
| 6 | `BaseFileReader` + `SetBaseGoMod`/`SetBaseGoSum` | `differ.go`, `options.go`, `gta.go` | Low | - |
| 7 | Replace nuclear go.mod handling | `gta.go`, integration tests | High | 1, 5, 6 |

---

## 4. Commit 1: Add isLocalPackage and LocalImportersOf to packageContext

**Goal**: Foundation helpers for distinguishing local vs. external packages.

### Code Changes

**`packager.go`** -- Add two methods on `*packageContext` (after existing methods):

```go
// isLocalPackage returns true if the import path belongs to a main
// (local/workspace) module.
func (p *packageContext) isLocalPackage(importPath string) bool {
    for _, modPath := range p.modulesNamesByDir {
        if importPath == modPath || strings.HasPrefix(importPath, modPath+"/") {
            return true
        }
    }
    return false
}

// LocalImportersOf returns the import paths of local packages that
// import any package from the given module paths.
func (p *packageContext) LocalImportersOf(modulePaths []string) []string {
    moduleSet := make(map[string]struct{}, len(modulePaths))
    for _, mp := range modulePaths {
        moduleSet[mp] = struct{}{}
    }

    var importers []string
    for pkgPath, imports := range p.forward {
        if !p.isLocalPackage(pkgPath) {
            continue
        }
        for dep := range imports {
            for mp := range moduleSet {
                if dep == mp || strings.HasPrefix(dep, mp+"/") {
                    importers = append(importers, pkgPath)
                    goto nextPkg
                }
            }
        }
    nextPkg:
    }
    return importers
}
```

### Tests

**`packager_test.go`** -- Add:

#### `TestIsLocalPackage`

| Case | moduleNamesByDir | Import Path | Expected |
|---|---|---|---|
| local exact match | `{"/repo": "mymod"}` | `"mymod"` | `true` |
| local subpackage | `{"/repo": "mymod"}` | `"mymod/pkg/foo"` | `true` |
| external package | `{"/repo": "mymod"}` | `"golang.org/x/text"` | `false` |
| stdlib package | `{"/repo": "mymod"}` | `"fmt"` | `false` |
| similar prefix (no match) | `{"/repo": "mymod"}` | `"mymod2/foo"` | `false` |
| workspace multi-module | `{"/ws/a": "ws/a", "/ws/b": "ws/b"}` | `"ws/a/pkg"` | `true` |
| empty moduleNamesByDir | `{}` | `"mymod/pkg"` | `false` |

#### `TestLocalImportersOf`

Setup a `packageContext` with mock `forward` graph and `modulesNamesByDir`:

| Changed Modules | Forward Graph | Expected Importers |
|---|---|---|
| `["ext/foo"]` | `{"local/a": {"ext/foo/sub": {}}}` | `["local/a"]` |
| `["ext/foo"]` | `{"local/a": {"ext/foo": {}}, "local/b": {"ext/foo": {}}}` | `["local/a", "local/b"]` |
| `["ext/foo"]` | `{"local/a": {"ext/bar": {}}}` | `[]` |
| `["ext/foo"]` | `{"ext/baz": {"ext/foo": {}}}` | `[]` (ext skipped) |
| `["ext/foo", "ext/bar"]` | `{"local/a": {"ext/foo": {}}, "local/b": {"ext/bar": {}}}` | `["local/a", "local/b"]` |

### Validation

```bash
go test ./... && go vet ./...
```

### Commit Message

```
Add isLocalPackage and LocalImportersOf helpers to packageContext

These methods enable distinguishing local (main module) packages from
external dependencies and mapping changed dependency modules to the
local packages that import them. Foundation for unvendored repo support.
```

---

## 5. Commit 2: Skip Packages with Load Errors in Graph Construction

**Goal**: Prevent broken external packages from polluting the dependency graphs.

### Code Changes

**`packager.go`** -- In `addPackage()` (around line 301-310), after the `seen` check and `moduleNamesByDir` population, add:

```go
// Skip packages with load errors (e.g., unresolvable external deps).
// The module info has already been recorded above.
if len(pkg.Errors) > 0 {
    return
}
```

**Important**: This check must come AFTER `moduleNamesByDir` population (line 306-308) but BEFORE adding the package to the forward/reverse graphs.

### Tests

**`packager_test.go`** -- Add:

#### `TestAddPackage_SkipsErroredPackages`

Verify through `dependencyGraph()` behavior: when a package has `pkg.Errors` populated, it should not appear in the forward or reverse graph adjacency lists. Test this by constructing a scenario where `packages.Load` returns packages with errors (or by testing through the `GTA.ChangedPackages()` flow with a mock packager).

### Validation

```bash
go test ./... && go vet ./...
```

Verify existing tests pass unchanged -- vendored repos have no packages with errors.

### Commit Message

```
Skip packages with load errors in dependency graph construction

When packages.Load returns packages with errors (common for external
dependencies in unvendored repos), skip them when building the
forward/reverse dependency graphs. Module info is still recorded.
```

---

## 6. Commit 3: Tolerate PackageFromImport Errors for External Packages

**Goal**: Prevent `ChangedPackages()` from failing when the reverse graph contains external packages that can't be resolved.

### Code Changes

**`gta.go`** -- In `ChangedPackages()` around line 199-203, change:

```go
// BEFORE:
pkg2, err := packageFromImport(path)
if err != nil {
    return nil, err
}

// AFTER:
pkg2, err := packageFromImport(path)
if err != nil {
    // Package cannot be resolved -- likely an external
    // dependency not in the local module. Skip it.
    continue
}
```

### Tests

**Add test** in the appropriate test file:

#### `TestChangedPackages_ExternalPackageSkipped`

- Use `testPackager` with a graph that includes an external package as a dependent
- Make `PackageFromImport` return an error for the external path
- Verify `ChangedPackages()` succeeds and only contains local packages
- Verify the external package is NOT in `AllChanges`

### Validation

```bash
go test ./... && go vet ./...
```

**Backward compatibility check**: Existing tests must pass. For vendored repos, `PackageFromImport` always succeeds for packages in the graph, so the `continue` path is never hit.

### Commit Message

```
Tolerate PackageFromImport errors for unresolvable dependents

When a package in the dependency graph cannot be resolved (e.g., an
external dependency in an unvendored repo), skip it instead of
returning an error from ChangedPackages(). This prevents GTA from
crashing on unvendored repositories.
```

---

## 7. Commit 4: Filter Non-Local Dependents from Graph Traversal

**Goal**: Ensure the graph traversal in `markedPackages()` doesn't visit external package nodes.

### Code Changes

**`gta.go`** -- Add an unexported interface and use it in `traverse()`:

```go
// localPackageChecker is satisfied by packageContext but not required
// by custom Packager implementations.
type localPackageChecker interface {
    isLocalPackage(string) bool
}
```

In the `traverse` function within `markedPackages()` (lines 444-463), add filtering:

```go
traverse = func(node string) {
    if marked[node] {
        return
    }
    marked[node] = true

    if edges, ok := graph.graph[node]; ok {
        for edge := range edges {
            // Skip non-local dependents if the packager supports the check
            if checker, ok := g.packager.(localPackageChecker); ok {
                if !checker.isLocalPackage(edge) {
                    continue
                }
            }
            traverse(edge)
        }
    }
    // ... testOnlyGraph traversal (same filter applied)
}
```

### Tests

**Add test**:

#### `TestMarkedPackages_SkipsExternalDependents`

- Set up a `packageContext` with a forward/reverse graph containing both local and external edges
- Verify `markedPackages()` results do not include external packages
- Verify local packages are still properly traversed

### Validation

```bash
go test ./... && go vet ./...
```

**Backward compatibility**: When using a custom `Packager` that doesn't implement `localPackageChecker`, the type assertion fails and traversal includes all edges (existing behavior preserved).

### Commit Message

```
Filter non-local dependents from reverse graph traversal

When traversing the dependency graph in markedPackages(), skip edges
to external (non-local) packages. Uses a type assertion so custom
Packager implementations retain the existing traversal behavior.
```

---

## 8. Commit 5: Add go.mod and go.sum Diff Functions

**Goal**: Pure-function library for parsing and diffing go.mod/go.sum files.

### Code Changes

**`gomod.go`** (new file):

```go
package gta

import (
    "strings"
    "golang.org/x/mod/modfile"
)

// ModuleChange describes a change to a dependency module.
type ModuleChange struct {
    Path       string // module path, e.g. "golang.org/x/text"
    OldVersion string // empty if newly added
    NewVersion string // empty if removed
}

func diffGoMod(oldData, newData []byte) ([]ModuleChange, error) { ... }
func diffGoSum(oldData, newData []byte) []string { ... }
func parseGoSumEntries(data []byte) map[string]struct{} { ... }
func goSumModulePath(line string) string { ... }
```

See `docs/unvendored-repo-support-plan.md` sections 5.4.2-5.4.3 for full implementations.

### Tests

**`gomod_test.go`** (new file):

#### `TestDiffGoMod` -- Table-Driven

| Case | Old go.mod | New go.mod | Expected |
|---|---|---|---|
| version bump | `require foo v1.0.0` | `require foo v1.1.0` | `{Path: "foo", Old: "v1.0.0", New: "v1.1.0"}` |
| dependency added | (none) | `require bar v1.0.0` | `{Path: "bar", New: "v1.0.0"}` |
| dependency removed | `require baz v1.0.0` | (none) | `{Path: "baz", Old: "v1.0.0"}` |
| no change | `require foo v1.0.0` | `require foo v1.0.0` | `[]` |
| multiple changes | 3 deps | 2 changed, 1 added, 1 removed | 4 changes |
| indirect to direct (same version) | `foo v1.0.0 // indirect` | `foo v1.0.0` | `[]` (version unchanged) |
| replace added | (none) | `replace foo => ../local` | `{Path: "foo"}` |
| replace version changed | `replace foo v1 => bar v1` | `replace foo v1 => bar v2` | `{Path: "foo"}` |
| replace removed | `replace foo => ../local` | (none) | `{Path: "foo"}` |
| malformed old go.mod | `!!!invalid` | valid | error |
| malformed new go.mod | valid | `!!!invalid` | error |
| both empty modules | no requires | no requires | `[]` |

#### `TestDiffGoSum` -- Table-Driven

| Case | Old go.sum | New go.sum | Expected Module Paths |
|---|---|---|---|
| version added | (empty) | `golang.org/x/text v0.4.0 h1:abc` | `["golang.org/x/text"]` |
| hash changed | `x/text v0.3.0 h1:abc` | `x/text v0.3.0 h1:def` | `["x/text"]` |
| version removed | `x/text v0.3.0 h1:abc` | (empty) | `["x/text"]` |
| no change | identical | identical | `[]` |
| transitive dep added | `a v1 h1:x` | `a v1 h1:x\nb v1 h1:y` | `["b"]` |
| multiple changes | 2 entries | 2 different entries | 2 module paths |
| both empty | (empty) | (empty) | `[]` |

#### `TestGoSumModulePath`

| Input | Expected |
|---|---|
| `"golang.org/x/text v0.4.0 h1:abc"` | `"golang.org/x/text"` |
| `"foo v1.0.0/go.mod h1:abc"` | `"foo"` |
| `""` | `""` |

### Validation

```bash
go test ./... && go vet ./...
```

No existing tests affected -- purely additive.

### Commit Message

```
Add go.mod and go.sum diff analysis functions

New file gomod.go provides diffGoMod() and diffGoSum() for precise
dependency change detection. diffGoMod compares Require and Replace
directives using modfile.ParseLax. diffGoSum does line-level comparison
to catch transitive dependency changes.
```

---

## 9. Commit 6: Add BaseFileReader Interface and Options

**Goal**: Provide the mechanism to retrieve old go.mod/go.sum content from git merge-base.

### Code Changes

**`differ.go`** -- Add after the `Differ` interface:

```go
// BaseFileReader provides access to file content at the base branch/commit.
type BaseFileReader interface {
    ReadBaseFile(relativePath string) ([]byte, error)
}
```

Implement on the `git` struct:
- Store `root string` (git repo root, already computed in `diff()` via `git rev-parse --show-toplevel`)
- Store `baseRef string` (already computed in `diff()` via `branchPointOf`)
- `ReadBaseFile` calls `git show <baseRef>:<relativePath>`
- The `differ` wrapper delegates to the `git` struct when available

**`options.go`** -- Add:

```go
// SetBaseGoMod provides old go.mod content for dependency diff analysis
// when using a file differ (no git access).
func SetBaseGoMod(path string) Option {
    return func(g *GTA) error {
        g.baseGoMod = path
        return nil
    }
}

// SetBaseGoSum provides old go.sum content for dependency diff analysis
// when using a file differ (no git access).
func SetBaseGoSum(path string) Option {
    return func(g *GTA) error {
        g.baseGoSum = path
        return nil
    }
}
```

**`gta.go`** -- Add fields to `GTA` struct:

```go
type GTA struct {
    // ... existing fields ...
    baseGoMod string
    baseGoSum string
}
```

### Tests

**`differ_test.go`** -- Add:

#### `TestBaseFileReaderInterface`

- Verify `NewGitDiffer()` returns a value that satisfies `BaseFileReader` via type assertion
- Verify `NewFileDiffer()` does NOT satisfy `BaseFileReader`

### Validation

```bash
go test ./... && go vet ./...
```

### Commit Message

```
Add BaseFileReader interface and SetBaseGoMod/SetBaseGoSum options

BaseFileReader enables reading file content at the git merge-base,
used to retrieve old go.mod/go.sum for dependency diff analysis.
SetBaseGoMod and SetBaseGoSum options provide the same capability
for file-differ mode (no git access).
```

---

## 10. Commit 7: Replace Mark-All with Precise Dependency Diff

**Goal**: The integration commit. Replace the nuclear "mark all packages when go.mod changes" with precise diff-based analysis.

### Code Changes

**`gta.go`** -- Rewrite the `hasModuleConfig` block (lines 266-293):

```go
if hasModuleConfig {
    var changedModPaths []string
    preciseDetection := false

    // Strategy 1: Use BaseFileReader (git differ) to get old content
    if baseReader, ok := g.differ.(BaseFileReader); ok {
        for _, f := range dir.Files {
            if f == "go.mod" {
                relPath := relativeGoModPath(abs, g.roots)
                oldData, err := baseReader.ReadBaseFile(relPath)
                if err == nil && oldData != nil {
                    newData, _ := os.ReadFile(filepath.Join(abs, "go.mod"))
                    if changes, err := diffGoMod(oldData, newData); err == nil {
                        for _, mc := range changes {
                            changedModPaths = append(changedModPaths, mc.Path)
                        }
                        preciseDetection = true
                    }
                }
            }
            if f == "go.sum" {
                relPath := relativeGoSumPath(abs, g.roots)
                oldData, err := baseReader.ReadBaseFile(relPath)
                if err == nil && oldData != nil {
                    newData, _ := os.ReadFile(filepath.Join(abs, "go.sum"))
                    sumPaths := diffGoSum(oldData, newData)
                    changedModPaths = append(changedModPaths, sumPaths...)
                    preciseDetection = true
                }
            }
        }
    }

    // Strategy 2: Use provided base files (file-differ mode)
    if !preciseDetection && (g.baseGoMod != "" || g.baseGoSum != "") {
        if g.baseGoMod != "" {
            oldData, _ := os.ReadFile(g.baseGoMod)
            newData, _ := os.ReadFile(filepath.Join(abs, "go.mod"))
            if changes, err := diffGoMod(oldData, newData); err == nil {
                for _, mc := range changes {
                    changedModPaths = append(changedModPaths, mc.Path)
                }
                preciseDetection = true
            }
        }
        if g.baseGoSum != "" {
            oldData, _ := os.ReadFile(g.baseGoSum)
            newData, _ := os.ReadFile(filepath.Join(abs, "go.sum"))
            sumPaths := diffGoSum(oldData, newData)
            changedModPaths = append(changedModPaths, sumPaths...)
            preciseDetection = true
        }
    }

    // Apply results
    if preciseDetection && len(changedModPaths) > 0 {
        changedModPaths = dedup(changedModPaths)
        if finder, ok := g.packager.(localImporterFinder); ok {
            for _, importerPath := range finder.LocalImportersOf(changedModPaths) {
                changed[importerPath] = false
            }
        }
    } else if !preciseDetection {
        // Fallback: no base available -- use nuclear option (existing behavior)
        graph, err := g.packager.DependentGraph()
        if err == nil {
            for pkg := range graph.graph {
                if !hasPrefixIn(pkg, g.prefixes) {
                    continue
                }
                if _, err := g.packager.PackageFromImport(pkg); err == nil {
                    changed[pkg] = false
                }
            }
        }
    }

    if !hasGoFile(dir.Files) {
        continue
    }
}
```

Add helper functions:

```go
// dedup removes duplicate strings from a slice.
func dedup(sl []string) []string { ... }

// relativeGoModPath computes the git-relative path to a go.mod file.
func relativeGoModPath(absDir string, roots []string) string { ... }
```

**NOTE**: `go.work` changes continue to use the nuclear option. The `go.work` case should be separated from `go.mod` -- check for `go.work` first and handle it with existing behavior, then handle `go.mod`/`go.sum` with precise detection.

### Tests

#### `TestMarkedPackages_GoModChange_PreciseDetection`

- Mock a `BaseFileReader`-implementing differ that returns old go.mod content
- Simulate go.mod change with a version bump for module X
- Set up a packager where only package A imports module X, packages B and C don't
- Verify only package A is marked, not B or C

#### `TestMarkedPackages_GoModChange_FallbackToNuclear`

- Use a differ that does NOT implement `BaseFileReader`
- Don't set `baseGoMod`/`baseGoSum` options
- Verify ALL packages are marked (existing nuclear behavior preserved)

#### `TestMarkedPackages_GoSumChange_TransitiveDep`

- Simulate go.sum-only change (go.mod unchanged)
- Verify correct local importers are marked via go.sum diff

#### `TestMarkedPackages_GoWorkChange_StillNuclear`

- Verify go.work changes still trigger the nuclear option (not the precise path)

#### `TestMarkedPackages_BaseGoModOption`

- Use `SetBaseGoMod()` option with a file differ
- Verify precise detection works without git

### Validation

```bash
go test ./... && go vet ./...
```

**Critical checks**:
- All existing tests pass (backward compatibility)
- go.work tests still pass (nuclear behavior preserved for go.work)
- Vendored repo tests still pass

### Commit Message

```
Replace mark-all go.mod handling with precise dependency diff analysis

When go.mod or go.sum changes in an unvendored repo, GTA now parses
the diff to identify exactly which dependency modules changed, then
marks only the local packages that import those modules. This replaces
the previous "mark all packages" approach which negated GTA's value.

Falls back to mark-all when base file content is unavailable (e.g.,
custom Differ without BaseFileReader, no SetBaseGoMod option).

go.work changes continue to use mark-all since workspace structural
changes warrant full re-evaluation.
```

---

## 11. Test Fixtures

### `testdata/gomoddiff/` (created in Commit 5)

Inline `[]byte` test data in `gomod_test.go` matching the pattern used in `differ_test.go`. No separate fixture files needed for pure-function unit tests.

### `testdata/unvendoredtest/` (created in Commit 7)

```
testdata/unvendoredtest/
    go.mod              # module unvendoredtest; require github.com/ext/foo v1.0.0
    pkg/
        a/
            a.go        # package a; import "github.com/ext/foo"; import "unvendoredtest/pkg/b"
        b/
            b.go        # package b; import "fmt"
        c/
            c.go        # package c; import "github.com/ext/bar"
```

### Existing fixtures (unchanged)

- `testdata/gtatest/` -- used by existing `TestGTA_ChangedPackages`
- `gtaintegration/testdata/` -- used by existing integration tests

---

## 12. Validation Gates

### Gate 1: After Commits 1-4 (Phase 1 Complete)

All existing tests pass PLUS:

- [ ] `TestIsLocalPackage` passes (all 7 cases)
- [ ] `TestLocalImportersOf` passes (all 5 cases)
- [ ] `TestAddPackage_SkipsErroredPackages` passes
- [ ] `TestChangedPackages_ExternalPackageSkipped` passes
- [ ] `TestMarkedPackages_SkipsExternalDependents` passes
- [ ] `go vet ./...` clean
- [ ] GTA does not crash on unvendored repos (manual smoke test if possible)

### Gate 2: After Commit 5 (Diff Functions)

Gate 1 PLUS:

- [ ] `TestDiffGoMod` passes (all 12 cases)
- [ ] `TestDiffGoSum` passes (all 7 cases)
- [ ] `TestGoSumModulePath` passes

### Gate 3: After Commit 6 (BaseFileReader)

Gate 2 PLUS:

- [ ] `TestBaseFileReaderInterface` passes
- [ ] New options compile and can be set without error

### Gate 4: After Commit 7 (Integration -- Pre-Merge)

Gate 3 PLUS:

- [ ] `TestMarkedPackages_GoModChange_PreciseDetection` passes
- [ ] `TestMarkedPackages_GoModChange_FallbackToNuclear` passes
- [ ] `TestMarkedPackages_GoSumChange_TransitiveDep` passes
- [ ] `TestMarkedPackages_GoWorkChange_StillNuclear` passes
- [ ] `TestMarkedPackages_BaseGoModOption` passes
- [ ] Full suite: `go test -v -count=1 -race ./...`
- [ ] `go vet ./...` clean
- [ ] All 11 existing test files pass unchanged
- [ ] Backward compatibility confirmed for vendored repos

---

## 13. Regression Checklist

### Existing Tests That Must Continue Passing

| Test File | Key Tests | What They Protect |
|---|---|---|
| `gta_test.go` | `TestGTA_ChangedPackages` (12+ subtests) | Core changed-package detection |
| `differ_test.go` | `Test_diffFileDirectories` | Git diff path parsing |
| `graph_test.go` | `TestGraphTraversal` | Graph traversal correctness |
| `packager_test.go` | `TestPackageContextImplementsPackager` | Interface compliance |
| `helpers_test.go` | `Setenv`, `chdir` | Test infrastructure |
| `gtaintegration/` | 6 integration tests | End-to-end with real git repos |

### Specific Regression Scenarios

1. **Vendored repo + go.mod change**: Must still mark all packages (nuclear option). If `BaseFileReader` is available but the repo is vendored, the precise path may run but should produce equivalent results.

2. **`testPackager` compilation**: Adding `LocalImportersOf` to `packageContext` only (not the interface) means `testPackager` in `gta_test.go` does NOT need updating. If we accidentally add it to the `Packager` interface, compilation will fail -- this is our safety net.

3. **`PackageFromImport` error tolerance**: The `continue` in Commit 3 must NOT mask legitimate errors for local packages. If the packager supports `isLocalPackage`, check it before deciding to continue vs. error.

4. **go.work still uses nuclear option**: The go.work case must be tested separately and must not enter the precise diff path.

---

## 14. Rollback Strategy

### Per-Commit Rollback

| Commit | Rollback | Side Effects |
|---|---|---|
| 1 | `git revert` | Commits 4 and 7 would need revert too |
| 2 | `git revert` | Independent, no cascading |
| 3 | `git revert` | Independent, no cascading |
| 4 | `git revert` | Independent (but loses non-local filtering) |
| 5 | `git revert` | Commit 7 would need revert too |
| 6 | `git revert` | Commit 7 would need revert too |
| 7 | `git revert` | Restores nuclear go.mod behavior; commits 1-6 remain safe |

### Feature Rollback

If the entire feature needs to be backed out, revert in reverse order: 7, 6, 5, 4, 3, 2, 1.

### Partial Rollback (Defensive Only)

To keep just the crash-prevention (Phase 1) without precise diff detection: revert only commit 7. Commits 1-6 remain and GTA will:
- Not crash on unvendored repos (commits 2, 3, 4)
- Still use nuclear option for go.mod changes (commit 7 reverted)
- Have diff functions available but unused (commits 5, 6)

---

## 15. Commit Dependency Graph

```
Commit 1 (isLocalPackage + LocalImportersOf) ─────┐
    │                                               │
    ├──► Commit 4 (filter non-local traversal)      │
    │                                               │
    └───────────────────────────────────────────────┼──► Commit 7 (integration)
                                                    │         ▲
Commit 2 (skip pkg.Errors) ── independent           │         │
                                                    │         │
Commit 3 (tolerate errors) ── independent           │         │
                                                    │         │
Commit 5 (gomod.go diff) ─────────────────────────┘         │
                                                              │
Commit 6 (BaseFileReader + options) ──────────────────────────┘
```

**Parallel-safe ordering**: Commits 2, 3, 5, and 6 are mutually independent and could be implemented in any order. Commit 4 requires Commit 1. Commit 7 requires Commits 1, 5, and 6.

**Recommended implementation order**: 1 -> 2 -> 3 -> 4 -> 5 -> 6 -> 7 (sequential for clarity, but 2/3 and 5/6 can be parallelized).
