# Unvendored Repository Support for GTA

## Design, Implementation, and Test Plan

**Date**: 2026-03-27
**Status**: Proposal (not yet implemented)
**Depends on**: Go workspace support (merged in `6c16b44`)
**Upstream tracking**: [digitalocean/gta#58](https://github.com/digitalocean/gta/issues/58)

---

## Table of Contents

1. [Problem Statement](#1-problem-statement)
2. [Current Behavior Analysis](#2-current-behavior-analysis)
3. [Upstream Discussion Summary](#3-upstream-discussion-summary)
4. [Technical Research Findings](#4-technical-research-findings)
5. [Design](#5-design)
6. [Implementation Plan](#6-implementation-plan)
7. [Test Plan](#7-test-plan)
8. [Risks and Mitigations](#8-risks-and-mitigations)
9. [References](#9-references)

---

## 1. Problem Statement

GTA was built with the assumption that dependencies are vendored (`go mod vendor`). When a repository does **not** vendor its dependencies, GTA fails in two ways:

1. **`packages.Load` fails**: Without a vendor directory, `packages.Load(cfg, "...")` attempts to resolve all transitive dependencies via the module cache. If the cache is not populated, it returns errors like `"updates to go.mod needed"` or package-not-found errors. This causes GTA to abort entirely.

2. **Dependency changes are not tracked precisely**: When `go.mod` changes (dependency version bumps, additions, removals), the existing code marks _all_ prefix-matching packages as changed -- the "nuclear option." This negates GTA's core value of identifying _only_ the affected subset. Furthermore, transitive dependency changes (recorded in `go.sum` but not necessarily in `go.mod`) are not detected at all.

The goal is to make GTA work correctly with unvendored repositories by:
- **Detecting when a package is not local** and skipping it rather than failing
- **Parsing go.mod diffs** to identify changed direct dependencies
- **Parsing go.sum diffs** to identify changed transitive dependencies
- **Marking only the local packages that import changed dependencies** as affected, triggering the same dependency-tree tracking that a modified local package would trigger

---

## 2. Current Behavior Analysis

### 2.1 Where Vendoring Is Assumed

| Location | What Happens | Impact Without Vendor |
|----------|-------------|----------------------|
| `packager.go:283` | `packages.Load(cfg, "...")` loads ALL packages including external deps | Fails if deps aren't cached |
| `packager.go:301-388` | `addPackage()` adds ALL loaded packages to forward/reverse graphs with no local/external filtering | External packages bloat graphs |
| `packager.go:423-435` | `stripVendor()` strips `/vendor/` from import paths | Harmless -- returns path unchanged when no vendor segment |
| `packager.go:243-250` | `resolveLocal()` handles vendor path segments | Harmless -- conditional only fires if vendor prefix found |
| `packager.go:160-161` | `PackageFromImport()` returns error if package not in forward graph | Fails when trying to resolve external dep not loaded by `packages.Load` |
| `gta.go:264` | TODO comment: `handle changes to go.mod when vendoring is not being used` | Acknowledged gap from upstream maintainer |
| `gta.go:266-293` | go.mod/go.work change detection marks ALL resolvable packages as changed | Over-reports; negates GTA's value |

### 2.2 The `packages.Load` Problem

The config at `packager.go:84-102`:

```go
cfg := &packages.Config{
    Mode: packages.NeedName | packages.NeedFiles | packages.NeedEmbedFiles |
          packages.NeedImports | packages.NeedDeps | packages.NeedModule |
          packages.NeedForTest,
    Tests: true,
}
```

Key behaviors without a vendor directory:

| `-mod` flag | Default when | Behavior |
|-------------|-------------|----------|
| `-mod=vendor` | vendor/ exists + go 1.14+ | Uses vendor exclusively. **Current assumption.** |
| `-mod=readonly` | **no vendor/ exists** | Uses module cache. Fails if go.mod needs updates. Will NOT download. |
| `-mod=mod` | explicitly set | Uses module cache. Will modify go.mod and download as needed. |

Without vendoring, Go defaults to `-mod=readonly`. If the module cache (`$GOPATH/pkg/mod`) is not populated, `packages.Load` returns packages with errors in `pkg.Errors` rather than a top-level error.

### 2.3 The Dependency Graph Problem

In `dependencyGraph()` (`packager.go:301-388`), the `addPackage()` function recurses into ALL packages returned by `packages.Load` -- including external dependencies. This means:

- The `forward` graph maps every package to its imports (including stdlib and external)
- The `reverse` graph maps every imported package to its importers
- When GTA traverses the reverse graph from a changed package, it may attempt to resolve external packages via `PackageFromImport()`, which fails with `"<pkg> not found"` if the external package wasn't loaded

**This is the root cause** of the error reported in upstream issue #27 and #58.

### 2.4 How `packages.Package.Module` Distinguishes Local vs External

Every package loaded by `packages.Load` with `NeedModule` has a `Module` field:

```go
type Module struct {
    Path    string  // e.g. "github.com/digitalocean/gta"
    Main    bool    // true for workspace/main modules
    Dir     string  // absolute path on disk
    Version string  // empty for main modules
}
```

**The canonical check**:
- `pkg.Module != nil && pkg.Module.Main` = **local** (main module or workspace module)
- `pkg.Module != nil && !pkg.Module.Main` = **external dependency**
- `pkg.Module == nil` = **standard library**

GTA already uses this check at `packager.go:306-307` to populate `moduleNamesByDir`, but does not use it to _filter_ the dependency graphs.

---

## 3. Upstream Discussion Summary

### 3.1 Issue #58: Core Tracking Issue

Maintainer `@bhcleek` outlined concerns and potential paths forward:

> "Supporting use of gta without vendored dependencies could easily lead to situations where gta is not correctly determining which files have changed, because it is unaware of the files that changed in dependencies."

He proposed two flag-based approaches:
1. A flag providing the `go.mod` from the merge-base
2. A flag providing the module version for the merge-base

Both would allow `go mod download` to populate the cache, enabling full analysis.

**Source**: https://github.com/digitalocean/gta/issues/58

### 3.2 Issue #27: The Original Problem Report

Key insight from `@doriandekoning`:

> "If a dependency changes the go mod/sum files change, those are tracked by version control. Why wouldn't it be possible to use those to check for changes?"

This insight is directly applicable: `go.sum` contains cryptographic hashes for **every module version** in the resolved dependency graph, including transitive dependencies. Diffing `go.sum` between the base and HEAD provides a complete picture of all dependency changes -- both direct and transitive.

`@matthewd98` provided a complete shell-script workaround that forks both git refs and vendors each before diffing.

**Source**: https://github.com/digitalocean/gta/issues/27

### 3.3 Upstream Position Summary

- The maintainer considers vendoring the "correct" approach
- The maintainer is open to supporting unvendored repos but wants precision
- No implementation work has been done upstream
- The community has asked for this repeatedly since 2021

---

## 4. Technical Research Findings

### 4.1 go.mod Parsing with `modfile.Parse`

Already vendored at `vendor/golang.org/x/mod/modfile/rule.go` and already imported in `gta.go` for workspace support. Key types:

```go
type File struct {
    Module  *Module
    Require []*Require  // path + version
    Replace []*Replace  // old -> new mapping
    Exclude []*Exclude
}

type Require struct {
    Mod      module.Version  // {Path: "example.com/foo", Version: "v1.2.3"}
    Indirect bool
}

type Replace struct {
    Old module.Version
    New module.Version  // Version empty for filesystem replacements
}
```

To diff two go.mod files, parse both with `modfile.Parse`, build `map[string]string` (path -> version) from `Require` entries, and compare.

### 4.2 go.sum as a Signal for Transitive Dependency Changes

`go.sum` contains cryptographic hashes for every module version in the resolved dependency graph -- including transitive dependencies. Each line has the format:

```
golang.org/x/text v0.3.0 h1:abc123...
golang.org/x/text v0.3.0/go.mod h1:def456...
```

When any dependency changes (direct or transitive), `go.sum` will have new or changed entries. By diffing old vs new `go.sum`, we get the **complete set of module versions that changed**, covering the gap that go.mod diff alone cannot fill.

**Why go.sum closes the transitive gap**: Go uses Minimal Version Selection (MVS) to resolve dependency versions. `go.mod` only records minimum version requirements for direct dependencies, while `go.sum` records the actual resolved versions for the entire dependency graph. A transitive dependency change (e.g., dependency-of-dependency version bump) that doesn't affect `go.mod` will still appear as new entries in `go.sum`.

**Caveat**: `go.sum` is append-only until `go mod tidy` is run. New entries indicate "newly resolved version" which is a conservative but safe signal for marking importers as affected.

### 4.3 Getting Old go.mod and go.sum Content

The differ already has access to the git diff. To get old file content:

```bash
git show <merge-base>:go.mod
git show <merge-base>:go.sum
```

Or via `git show <merge-base>:<path-to-file>` for workspace modules where these files may be in subdirectories.

This fits naturally into the existing `Differ` interface by extending it or by adding a new method.

### 4.4 Mapping Changed Modules to Affected Local Packages

When `packages.Load` runs with `NeedModule | NeedImports | NeedDeps`, every loaded package knows:
- Its own module (`pkg.Module.Path`)
- Its imports, each with their module (`importedPkg.Module.Path`)

So given a set of changed dependency module paths, finding affected local packages is:

```
for each loaded local package (pkg.Module.Main == true):
    for each import of pkg:
        if import.Module.Path is in changedModules:
            mark pkg as changed
```

This is **package-level precision** -- far better than marking everything dirty.

### 4.5 Handling `packages.Load` Errors for External Packages

`packages.Load` does NOT return a top-level error when individual packages fail. Instead, errors are embedded in `pkg.Errors`:

```go
pkgs, err := packages.Load(cfg, "...")
// err is only non-nil for driver failures
for _, pkg := range pkgs {
    for _, e := range pkg.Errors {
        // e.Msg contains the error, e.Kind is ListError/ParseError/TypeError
    }
}
```

This means GTA can handle unresolvable external packages gracefully by checking `pkg.Errors` and skipping packages with errors rather than aborting.

### 4.6 Similar Tools

| Tool | Approach | Granularity |
|------|----------|-------------|
| [go-modiff](https://github.com/saschagrunert/go-modiff) | Compares go.mod between versions | Module-level |
| [gomod](https://github.com/Helcaraxan/gomod) | `rdeps` for reverse dependency analysis | Package-level |
| [goda](https://github.com/loov/goda) | Query language for package selection | Package-level |
| [knit](https://github.com/nicolasgere/knit) | Workspace-aware changed module detection | Module-level |

**No existing tool combines go.mod/go.sum diffing with package-level reverse dependency analysis.** This would be novel.

---

## 5. Design

### 5.1 Design Principles

1. **Don't fail on non-local packages** -- skip them gracefully
2. **Parse go.mod and go.sum diffs for precision** -- don't mark everything dirty
3. **Use go.sum to capture transitive dependency changes** -- not just direct ones
4. **Backward compatible** -- vendored repos work exactly as before
5. **No network access required** -- work with whatever is already cached
6. **Workspace-aware** -- handle go.mod/go.sum changes in any workspace module

### 5.2 Architecture Overview

The design introduces two changes:

**Change A: Filter non-local packages from dependency graphs**

In `dependencyGraph()`, after loading packages via `packages.Load`, filter the reverse dependency graph to exclude edges where the dependent is a non-local (external) package. This prevents GTA from trying to resolve external packages during `ChangedPackages()`.

The forward graph can retain external packages (they're needed to track what local packages import), but the reverse graph should only contain edges where both the key (imported package) AND the values (importing packages) are either local packages or external packages that are imported by local packages.

More precisely: when traversing dependents in `markedPackages()`, skip any dependent whose import path does not resolve via `PackageFromImport()`. This is already partially handled -- `PackageFromImport` returns an error and `ChangedPackages()` at line 199 propagates it. The fix is to **tolerate** the error instead of propagating it.

**Change B: Parse go.mod and go.sum diffs to identify changed dependencies**

When go.mod or go.sum is in the changed files list:

1. Retrieve old go.mod and go.sum content (from git merge-base or user-provided files)
2. Parse go.mod files with `modfile.Parse` and compare `Require`/`Replace` directives
3. Parse go.sum files as line-based `module version hash` entries and diff them
4. Combine results: the union of changed module paths from both go.mod and go.sum diffs
5. For each changed module path, find all local packages that import packages from that module (using the forward dependency graph + `pkg.Module.Path`)
6. Mark those local packages as changed (same as if their source files changed)
7. Let the existing reverse-dependency traversal propagate from there

This two-signal approach provides complete coverage:

| Signal | What it catches |
|--------|----------------|
| go.mod diff | Direct dependency changes (added, removed, version bumped) |
| go.sum diff | **All** dependency changes including transitive ones |
| Combined | Complete coverage of every dependency change in the resolved build graph |

### 5.3 Detailed Design: Change A -- Non-Local Package Filtering

#### 5.3.1 Add `isLocalPackage` check to `packageContext`

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
```

#### 5.3.2 Make `PackageFromImport` graceful for external packages

In `ChangedPackages()` at `gta.go:199`, when `PackageFromImport` returns an error, check if the package is external. If so, skip it rather than returning an error:

```go
pkg2, err := packageFromImport(path)
if err != nil {
    // If package is not in our local modules, skip it
    // rather than failing -- it's an external dependency.
    continue
}
```

#### 5.3.3 Filter reverse graph traversal

In `markedPackages()`, when traversing dependents via the graph, skip dependents that are external packages (they don't need to be tested/rebuilt).

### 5.4 Detailed Design: Change B -- Dependency Diff Analysis

#### 5.4.1 New type: `ModuleChange`

```go
// ModuleChange describes a change to a dependency.
type ModuleChange struct {
    Path       string // module path, e.g. "golang.org/x/text"
    OldVersion string // empty if newly added
    NewVersion string // empty if removed
}
```

#### 5.4.2 New function: `diffGoMod`

```go
// diffGoMod compares two go.mod file contents and returns the modules
// whose versions changed, were added, or were removed.
func diffGoMod(oldData, newData []byte) ([]ModuleChange, error) {
    oldFile, err := modfile.ParseLax("old/go.mod", oldData, nil)
    if err != nil {
        return nil, fmt.Errorf("parsing old go.mod: %w", err)
    }
    newFile, err := modfile.ParseLax("new/go.mod", newData, nil)
    if err != nil {
        return nil, fmt.Errorf("parsing new go.mod: %w", err)
    }

    oldReqs := make(map[string]string)
    for _, r := range oldFile.Require {
        oldReqs[r.Mod.Path] = r.Mod.Version
    }

    newReqs := make(map[string]string)
    for _, r := range newFile.Require {
        newReqs[r.Mod.Path] = r.Mod.Version
    }

    var changes []ModuleChange

    // Changed or added
    for path, newVer := range newReqs {
        oldVer, existed := oldReqs[path]
        if !existed {
            changes = append(changes, ModuleChange{Path: path, NewVersion: newVer})
        } else if oldVer != newVer {
            changes = append(changes, ModuleChange{Path: path, OldVersion: oldVer, NewVersion: newVer})
        }
    }

    // Removed
    for path, oldVer := range oldReqs {
        if _, exists := newReqs[path]; !exists {
            changes = append(changes, ModuleChange{Path: path, OldVersion: oldVer})
        }
    }

    // Also diff Replace directives
    oldReplace := make(map[string]string) // "old@version" -> "new@version"
    for _, r := range oldFile.Replace {
        key := r.Old.Path + "@" + r.Old.Version
        oldReplace[key] = r.New.Path + "@" + r.New.Version
    }
    newReplace := make(map[string]string)
    for _, r := range newFile.Replace {
        key := r.Old.Path + "@" + r.Old.Version
        newReplace[key] = r.New.Path + "@" + r.New.Version
    }
    for key, newTarget := range newReplace {
        oldTarget, existed := oldReplace[key]
        modPath := strings.SplitN(key, "@", 2)[0]
        if !existed || oldTarget != newTarget {
            changes = append(changes, ModuleChange{Path: modPath})
        }
    }
    for key := range oldReplace {
        if _, exists := newReplace[key]; !exists {
            modPath := strings.SplitN(key, "@", 2)[0]
            changes = append(changes, ModuleChange{Path: modPath})
        }
    }

    return changes, nil
}
```

#### 5.4.3 New function: `diffGoSum`

```go
// diffGoSum compares two go.sum file contents and returns the module
// paths whose resolved versions changed. This captures transitive
// dependency changes that go.mod diff alone would miss.
func diffGoSum(oldData, newData []byte) []string {
    oldEntries := parseGoSumEntries(oldData)
    newEntries := parseGoSumEntries(newData)

    changed := make(map[string]struct{})

    // New or changed entries
    for key := range newEntries {
        if _, ok := oldEntries[key]; !ok {
            modPath := goSumModulePath(key)
            changed[modPath] = struct{}{}
        }
    }

    // Removed entries
    for key := range oldEntries {
        if _, ok := newEntries[key]; !ok {
            modPath := goSumModulePath(key)
            changed[modPath] = struct{}{}
        }
    }

    var result []string
    for modPath := range changed {
        result = append(result, modPath)
    }
    return result
}

// parseGoSumEntries parses go.sum content into a set of
// "module version hash" lines for comparison.
func parseGoSumEntries(data []byte) map[string]struct{} {
    entries := make(map[string]struct{})
    for _, line := range strings.Split(string(data), "\n") {
        line = strings.TrimSpace(line)
        if line == "" {
            continue
        }
        entries[line] = struct{}{}
    }
    return entries
}

// goSumModulePath extracts the module path from a go.sum line.
// A go.sum line has the format: "module version hash"
func goSumModulePath(line string) string {
    fields := strings.Fields(line)
    if len(fields) >= 1 {
        return fields[0]
    }
    return line
}
```

#### 5.4.4 New function: `localImportersOf`

```go
// localImportersOf returns the import paths of local packages that
// import any package from the given module paths.
func (p *packageContext) localImportersOf(modulePaths []string) []string {
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
            // Check if this dependency belongs to a changed module
            // by prefix matching against module paths.
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

#### 5.4.5 Retrieve old go.mod and go.sum content

Extend the `Differ` interface or add a new method to retrieve file content at the merge-base:

```go
// BaseFileReader returns the content of files at the base branch/commit.
// Returns nil, nil if the file did not exist at the base.
type BaseFileReader interface {
    FileAtBase(relativePath string) ([]byte, error)
}
```

For the git differ, this calls:

```bash
git show <merge-base>:<relative-path>
```

For the file differ (used in `-changed-files` mode), new options provide old file paths:

```
-base-gomod=<path>
-base-gosum=<path>
```

#### 5.4.6 Integration point in `markedPackages()`

Replace the current "mark everything" block for go.mod/go.sum changes with:

```go
if hasModuleConfig {
    if baseReader, ok := g.differ.(BaseFileReader); ok {
        var changedModPaths []string

        // 1. Diff go.mod for direct dependency changes
        gomodRelPath := relativeToGitRoot(abs, "go.mod")
        oldModData, err := baseReader.FileAtBase(gomodRelPath)
        if err == nil && oldModData != nil {
            newModData, _ := os.ReadFile(filepath.Join(abs, "go.mod"))
            modChanges, _ := diffGoMod(oldModData, newModData)
            for _, mc := range modChanges {
                changedModPaths = append(changedModPaths, mc.Path)
            }
        }

        // 2. Diff go.sum for transitive dependency changes
        gosumRelPath := relativeToGitRoot(abs, "go.sum")
        oldSumData, err := baseReader.FileAtBase(gosumRelPath)
        if err == nil && oldSumData != nil {
            newSumData, _ := os.ReadFile(filepath.Join(abs, "go.sum"))
            sumChangedMods := diffGoSum(oldSumData, newSumData)
            changedModPaths = append(changedModPaths, sumChangedMods...)
        }

        // 3. Deduplicate and find local importers
        if len(changedModPaths) > 0 {
            changedModPaths = dedup(changedModPaths)
            for _, importerPath := range g.packager.LocalImportersOf(changedModPaths) {
                changed[importerPath] = false
            }
        }
    }

    // If this directory has no Go files (e.g. workspace root with
    // only go.work), skip further package-level processing.
    if !hasGoFile(dir.Files) {
        continue
    }
}
```

### 5.5 New Options

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `SetBaseGoMod(path string)` | `Option` | `""` | Path to the go.mod file from the base branch (for file-differ mode) |
| `SetBaseGoSum(path string)` | `Option` | `""` | Path to the go.sum file from the base branch (for file-differ mode) |

The `-base-gomod` and `-base-gosum` CLI flags map to these options.

### 5.6 Behavior Matrix

| Scenario | Vendor exists | go.mod/go.sum changed | Behavior |
|----------|:---:|:---:|----------|
| Vendored, no dependency change | Y | N | Existing behavior (unchanged) |
| Vendored, dependency changed | Y | Y | Existing behavior (mark all) -- fine because vendored deps are available |
| Unvendored, no dependency change | N | N | `packages.Load` uses module cache; skip unresolvable external packages |
| Unvendored, go.mod changed (direct dep) | N | Y | **New**: parse go.mod diff, find changed modules, mark local importers |
| Unvendored, go.sum changed (transitive dep) | N | Y | **New**: parse go.sum diff, find changed modules, mark local importers |
| Unvendored, both changed | N | Y | **New**: combine go.mod + go.sum diffs for complete coverage |
| Unvendored, packages.Load fails | N | * | Gracefully skip errored packages; warn user |

---

## 6. Implementation Plan

### Phase 1: Graceful External Package Handling (Required)

**Goal**: GTA doesn't crash on unvendored repos.

| Task | File | Description |
|------|------|-------------|
| 1.1 | `packager.go` | Add `isLocalPackage(importPath string) bool` to `packageContext` |
| 1.2 | `packager.go` | Add `LocalImportersOf(modulePaths []string) []string` to `Packager` interface and implement on `packageContext` |
| 1.3 | `gta.go:199` | In `ChangedPackages()`, tolerate `PackageFromImport` errors for non-local packages instead of returning the error |
| 1.4 | `packager.go:301-388` | In `addPackage()`, skip packages with non-empty `pkg.Errors` to avoid adding broken packages to graphs |
| 1.5 | `gta.go:412-450` | In `markedPackages()` graph traversal, skip dependents that are not local packages |

### Phase 2: Dependency Diff Analysis (Required)

**Goal**: Precise detection of affected packages when dependencies change.

| Task | File | Description |
|------|------|-------------|
| 2.1 | `gomod.go` (new) | Implement `diffGoMod(oldData, newData []byte) ([]ModuleChange, error)` with `Require` and `Replace` comparison |
| 2.2 | `gomod.go` | Implement `diffGoSum(oldData, newData []byte) []string` for transitive dependency change detection |
| 2.3 | `differ.go` | Add `BaseFileReader` interface; implement `FileAtBase` on git differ using `git show <merge-base>:<path>` |
| 2.4 | `gta.go:266-293` | Replace "mark all" handling with combined go.mod + go.sum diff + `LocalImportersOf` |
| 2.5 | `options.go` | Add `SetBaseGoMod(path string)` and `SetBaseGoSum(path string)` options |
| 2.6 | `cmd/gta/main.go` | Add `-base-gomod` and `-base-gosum` flags |

### Phase 3: Workspace Dependency Handling (Enhancement)

**Goal**: Handle go.mod/go.sum changes in any workspace module, not just the root.

| Task | File | Description |
|------|------|-------------|
| 3.1 | `gta.go` | When iterating changed directories, resolve which workspace module's go.mod/go.sum changed |
| 3.2 | `gta.go` | Apply `diffGoMod` + `diffGoSum` per-module within the workspace |

---

## 7. Test Plan

### 7.1 Unit Tests

#### Test: `TestDiffGoMod`

File: `gomod_test.go`

| Case | Old go.mod | New go.mod | Expected Changes |
|------|-----------|-----------|-----------------|
| Version bump | `require foo v1.0.0` | `require foo v1.1.0` | `{foo, v1.0.0, v1.1.0}` |
| Dependency added | (none) | `require bar v1.0.0` | `{bar, "", v1.0.0}` |
| Dependency removed | `require baz v1.0.0` | (none) | `{baz, v1.0.0, ""}` |
| No change | `require foo v1.0.0` | `require foo v1.0.0` | `[]` (empty) |
| Multiple changes | 3 deps | 2 changed, 1 added, 1 removed | 4 changes |
| Indirect to direct | `foo v1.0.0 // indirect` | `foo v1.0.0` | No change (version same) |
| Replace added | (none) | `replace foo => ../local` | `{foo, replace changed}` |
| Replace version changed | `replace foo v1 => bar v1` | `replace foo v1 => bar v2` | `{foo, replace changed}` |
| Replace removed | `replace foo => ../local` | (none) | `{foo, replace changed}` |

#### Test: `TestDiffGoSum`

File: `gomod_test.go`

| Case | Old go.sum | New go.sum | Expected Module Paths |
|------|-----------|-----------|----------------------|
| Version added | (no x/text) | `golang.org/x/text v0.4.0 h1:abc` | `["golang.org/x/text"]` |
| Version changed | `x/text v0.3.0 h1:abc` | `x/text v0.4.0 h1:def` | `["golang.org/x/text"]` |
| Version removed | `x/text v0.3.0 h1:abc` | (no x/text) | `["golang.org/x/text"]` |
| No change | identical | identical | `[]` |
| Transitive only | no go.mod change | new entry for dep-of-dep | `["indirect/dep"]` |
| Multiple changes | 2 entries | 2 different entries | 2 module paths |

#### Test: `TestIsLocalPackage`

| Import Path | moduleNamesByDir | Expected |
|-------------|-----------------|----------|
| `mymod/pkg/foo` | `{"/repo": "mymod"}` | `true` |
| `golang.org/x/text` | `{"/repo": "mymod"}` | `false` |
| `workspace.test/modA/pkg` | `{"/ws/modA": "workspace.test/modA", "/ws/modB": "workspace.test/modB"}` | `true` |

#### Test: `TestLocalImportersOf`

Setup forward graph:
- `mymod/api` imports `golang.org/x/text/language`, `mymod/internal/util`
- `mymod/cmd` imports `mymod/api`
- `mymod/internal/util` imports `github.com/pkg/errors`

| Changed Modules | Expected Importers |
|-----------------|-------------------|
| `["golang.org/x/text"]` | `["mymod/api"]` |
| `["github.com/pkg/errors"]` | `["mymod/internal/util"]` |
| `["golang.org/x/text", "github.com/pkg/errors"]` | `["mymod/api", "mymod/internal/util"]` |
| `["github.com/unused/dep"]` | `[]` |

### 7.2 Integration Tests

#### Test: `TestChangedPackages_UnvendoredRepo`

Setup:
- Create a temporary module with `go.mod` (no vendor directory)
- Module imports an external dep that is in the module cache
- Change a local .go file

Expected: GTA reports the changed package and its local dependents without crashing.

#### Test: `TestChangedPackages_GoModVersionBump`

Setup:
- Create a workspace with modules A, B
- Module A imports `golang.org/x/text`
- Differ reports `go.mod` changed with version bump of `golang.org/x/text`
- Provide old go.mod via `BaseFileReader`

Expected: Module A's package that imports `x/text` is marked changed, plus its transitive local dependents.

#### Test: `TestChangedPackages_GoSumTransitiveChange`

Setup:
- go.mod is unchanged (same `require` directives)
- go.sum has new entries for a transitive dependency (dep-of-dep version bump)
- A local package imports from a module that transitively depends on the changed module

Expected: The local package is marked changed because the go.sum diff identifies the transitive dependency change.

#### Test: `TestChangedPackages_GoModNewDependency`

Setup:
- Differ reports go.mod changed with a new `require` directive
- A local package imports from the new dependency

Expected: The local package is marked changed.

#### Test: `TestChangedPackages_GoModRemovedDependency`

Setup:
- Old go.mod has `require removed/dep v1.0.0`
- New go.mod doesn't have it
- A local package previously imported `removed/dep`

Expected: The local package is marked changed (import will fail, but the package is dirty).

#### Test: `TestChangedPackages_UnresolvableExternalDep`

Setup:
- Differ reports a changed .go file
- `packages.Load` returns errors for some external packages (e.g., not cached)
- The changed .go file's package has local dependents

Expected: GTA reports the changed package and its local dependents. External packages with errors are silently skipped.

#### Test: `TestFileAtBase`

Setup:
- Git repo with go.mod and go.sum at both HEAD and merge-base
- Both files changed between the two

Expected: `FileAtBase("go.mod")` and `FileAtBase("go.sum")` return the old content.

### 7.3 Test Fixtures

Create `testdata/unvendoredtest/`:

```
testdata/unvendoredtest/
    go.mod              # requires a fake external dep
    pkg/
        local.go        # imports the external dep
        local_test.go
    internal/
        helper.go       # imports local.go
    cmd/
        main.go         # imports internal/helper
```

And `testdata/gomoddiff/`:

```
testdata/gomoddiff/
    old.mod             # go.mod at merge-base
    new_version_bump.mod
    new_dep_added.mod
    new_dep_removed.mod
    new_replace_added.mod
    old.sum             # go.sum at merge-base
    new_transitive.sum  # go.sum with transitive dep change
    new_direct.sum      # go.sum with direct dep change
```

### 7.4 Validation Checklist

- [ ] All existing tests pass (no regressions)
- [ ] New unit tests for `diffGoMod` pass
- [ ] New unit tests for `diffGoSum` pass
- [ ] New unit tests for `isLocalPackage` pass
- [ ] New unit tests for `LocalImportersOf` pass
- [ ] Integration test with unvendored repo succeeds
- [ ] Integration test with go.mod version bump detects affected packages
- [ ] Integration test with go.sum transitive change detects affected packages
- [ ] GTA does not crash when external packages are unresolvable
- [ ] GTA produces correct results for vendored repos (backward compatible)
- [ ] `go vet ./...` passes
- [ ] `-base-gomod` and `-base-gosum` flags work in file-differ mode

---

## 8. Risks and Mitigations

### 8.1 Incomplete Module Cache

**Risk**: In CI without vendoring and without `go mod download`, the module cache may be empty. `packages.Load` may return errors for many packages.

**Mitigation**: Document that `go mod download` should be run before GTA in CI. When packages have errors, skip them and emit a warning rather than failing. The dependency diff approach does not require old dependency versions to be cached -- it only needs the current state plus the old go.mod/go.sum content from git.

### 8.2 go.sum Append-Only Behavior

**Risk**: `go.sum` is append-only until `go mod tidy` runs. Old version entries are retained even after upgrading. This could cause false positives: a new entry in go.sum doesn't necessarily mean a version is currently in use, only that it was resolved at some point.

**Mitigation**: This is acceptable for a conservative analysis tool. A false positive (marking a package as affected when it isn't) is preferable to a false negative (missing a genuine change). The over-reporting is bounded: it only affects packages that import from the module in question.

For repos that regularly run `go mod tidy` (which removes stale entries), this is a non-issue. For repos that don't, the worst case is marking a few extra packages as affected when a new entry appears that corresponds to a module already resolved at the same version.

### 8.3 False Positives from Dependency Version Bumps

**Risk**: A dependency version bump might not change the API surface, leading to unnecessary rebuilds.

**Mitigation**: This is inherent to any dependency-aware build system. The alternative (ignoring version bumps) risks missing genuine behavioral changes. The precision of marking only local importers (not all packages) already minimizes the blast radius.

### 8.4 `git show` Availability

**Risk**: The `BaseFileReader` approach requires git access. Users of `-changed-files` mode may not have git.

**Mitigation**: Provide the `-base-gomod` and `-base-gosum` flags as alternatives for file-differ mode. When neither git nor the flags are available, fall back to the current "mark all" behavior for go.mod/go.sum changes.

### 8.5 Performance of `packages.Load` Without Vendoring

**Risk**: Loading packages without vendoring may be slower due to module cache lookups.

**Mitigation**: Benchmarking needed. The `packages.Load` call is already the slowest part of GTA. Without vendoring, cache hits should be similarly fast; cache misses will fail fast with errors that we skip.

### 8.6 Upstream Compatibility

**Risk**: Our changes diverge from upstream, making future merges harder.

**Mitigation**: The changes are additive (new functions, new interface methods with default implementations) and the existing behavior is preserved for vendored repos. The `BaseFileReader` interface is optional -- the git differ implements it, but custom differs don't have to.

---

## 9. References

### Upstream Issues

| Issue | Title | URL |
|-------|-------|-----|
| #58 | Support repositories that do not have dependencies vendored | https://github.com/digitalocean/gta/issues/58 |
| #27 | Updates to go.mod needed | https://github.com/digitalocean/gta/issues/27 |
| #24 | Error listing changed packages in a nested module | https://github.com/digitalocean/gta/issues/24 |

### Go Documentation

| Resource | URL |
|----------|-----|
| Go Modules Reference (`-mod` flag) | https://go.dev/ref/mod |
| Minimal Version Selection | https://research.swtch.com/vgo-mvs |
| `golang.org/x/mod/modfile` API | https://pkg.go.dev/golang.org/x/mod/modfile |
| `golang.org/x/tools/go/packages` API | https://pkg.go.dev/golang.org/x/tools/go/packages |
| `packages.Package.Module` | https://pkg.go.dev/golang.org/x/tools/go/packages#Module |
| go.sum file format | https://go.dev/ref/mod#go-sum-files |

### Go Issues

| Issue | Description | URL |
|-------|-------------|-----|
| #33687 | `packages.Load` results depend on build cache state | https://github.com/golang/go/issues/33687 |
| #65816 | `packages.Package.Module` nil for stdlib | https://github.com/golang/go/issues/65816 |

### Similar Tools

| Tool | URL | Relevance |
|------|-----|-----------|
| go-modiff | https://github.com/saschagrunert/go-modiff | Compares go.mod between versions |
| gomod | https://github.com/Helcaraxan/gomod | Module analysis with `rdeps` for reverse deps |
| goda | https://github.com/loov/goda | Go dependency analysis toolkit |
| go-mod-diff | https://github.com/radeksimko/go-mod-diff | Comparison of Go dependencies |
| knit | https://github.com/nicolasgere/knit | Module-level workspace change detection (see `docs/knit-vs-gta-comparison.md`) |

### Source Code References (This Repo)

| File | Lines | Relevance |
|------|-------|-----------|
| `gta.go` | 264 | TODO comment: "handle changes to go.mod when vendoring is not being used" |
| `gta.go` | 266-293 | Current go.mod/go.work change handling ("mark all" approach) |
| `gta.go` | 196-201 | `PackageFromImport` error propagation in `ChangedPackages()` |
| `gta.go` | 410-450 | Graph traversal in `markedPackages()` |
| `packager.go` | 84-102 | `newLoadConfig()` -- `packages.Config` setup |
| `packager.go` | 160-161 | `PackageFromImport` -- "not found" error for missing packages |
| `packager.go` | 259-391 | `dependencyGraph()` -- builds forward/reverse graphs from `packages.Load` |
| `packager.go` | 301-388 | `addPackage()` -- recursive package addition with no local/external filter |
| `packager.go` | 306-307 | `pkg.Module.Main` check for `moduleNamesByDir` |
| `packager.go` | 423-435 | `stripVendor()` -- vendor path handling |
| `differ.go` | 166-237 | Git differ implementation |
| `vendor/golang.org/x/mod/modfile/rule.go` | * | `modfile.Parse`, `File`, `Require` types (already vendored) |
