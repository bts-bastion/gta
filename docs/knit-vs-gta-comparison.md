# Knit vs GTA: Comparative Analysis

## For the Use Case: "Only Build/Test Packages That Have Changed or Had a Dependency Change"

**Date**: 2026-03-27
**Knit**: https://github.com/nicolasgere/knit
**GTA**: https://github.com/digitalocean/gta (fork: bts-bastion/gta)

---

## Table of Contents

1. [Executive Summary](#1-executive-summary)
2. [Architecture Comparison](#2-architecture-comparison)
3. [Feature-by-Feature Comparison](#3-feature-by-feature-comparison)
4. [Dependency Change Detection](#4-dependency-change-detection)
5. [Precision Analysis](#5-precision-analysis)
6. [Strengths and Weaknesses](#6-strengths-and-weaknesses)
7. [Gap Analysis: What Each Tool Misses](#7-gap-analysis-what-each-tool-misses)
8. [Recommendation](#8-recommendation)
9. [References](#9-references)

---

## 1. Executive Summary

Knit and GTA solve the same core problem -- **identifying what needs rebuilding/retesting when code changes in a Go monorepo** -- but they take fundamentally different approaches:

| | Knit | GTA (with proposed changes) |
|-|------|----------------------------|
| **Granularity** | Module-level | Package-level |
| **Graph construction** | Shells out to `go list`, builds module DAG | Uses `packages.Load` API, builds package import graphs |
| **External deps** | Ignores entirely | Proposed: skip gracefully, track via go.mod diff |
| **go.mod changes** | Treats as any file change (marks containing module) | Proposed: parse diff, find exact local importers |
| **Vendoring** | Not required | Currently required; proposed changes remove requirement |
| **Workspace support** | Native (delegates to `go list -m`) | Implemented (parses `go.work`, multi-root) |
| **Codebase size** | ~1,040 lines | ~2,500+ lines |
| **Test-only deps** | Not distinguished | Separate test-only reverse graph |

**Bottom line**: GTA with the proposed unvendored support would be **strictly more capable** than Knit for the stated use case. GTA's package-level precision means it can tell you that only `services/api/handler` needs retesting (not all of `services/api`), while Knit can only say "module `services/api` is affected."

---

## 2. Architecture Comparison

### 2.1 How They Work

**Knit's pipeline** (~1,040 lines total):

```
git diff --name-only        → changed file paths
  ↓
filepath prefix match       → affected module directories
  ↓
go list -m -json            → enumerate workspace modules
  ↓
go list -json ./mod/...     → enumerate packages per module
  ↓
import prefix matching      → module-level dependency edges
  ↓
DFS traversal               → transitive affected modules
  ↓
sh -c "go test ./..."       → run tests per module
```

**GTA's pipeline** (~2,500+ lines):

```
git diff base...HEAD        → changed file paths (with branch-point detection)
  ↓
filepath.Dir grouping       → changed directories with file lists
  ↓
packages.Load(cfg, "...")   → ALL packages with full import graph
  ↓
recursive addPackage()      → forward + reverse + test-only-reverse graphs
  ↓
PackageFromDir per changed dir → changed package import paths
  ↓
reverse graph traversal     → transitive affected packages
  ↓
JSON or text output         → package import paths for `go test`
```

### 2.2 Key Architectural Difference

**Knit** uses a two-phase approach: enumerate modules first, then scan packages to build module edges. It collapses all package-level information to module granularity.

**GTA** loads the entire package graph in one shot via `packages.Load` and operates entirely at package granularity. It never "collapses" to modules -- the graph is always package-to-package.

### 2.3 Go Toolchain Integration

| Aspect | Knit | GTA |
|--------|------|-----|
| Module enumeration | `go list -m -json` (exec) | `packages.Load` with `NeedModule` |
| Package enumeration | `go list -json ./path/...` (exec) | `packages.Load` with `NeedImports \| NeedDeps` |
| Import resolution | JSON field `Imports []string` | `pkg.Imports` map of `*packages.Package` |
| Module association | `pkg.Module.Path` from JSON | `pkg.Module.Path` from `packages.Package` |
| Error handling | Fatal on any `go list` failure | Per-package `pkg.Errors` (graceful) |

GTA's use of the `packages.Load` API is more robust than Knit's shell-out approach because:
1. `packages.Load` returns per-package errors instead of failing entirely
2. It handles build constraints, CGO, and test files correctly
3. It provides the full recursive import graph in one call

---

## 3. Feature-by-Feature Comparison

### 3.1 Change Detection

| Feature | Knit | GTA |
|---------|------|-----|
| Git diff mechanism | `git diff --name-only <ref>` | `git diff base...HEAD --name-only --no-renames` |
| Merge-base support | `--merge-base` flag (manual) | Automatic branch-point detection via `git rev-list` |
| Merge commit support | No | Yes (`-merge` flag with parent detection) |
| Non-git mode | No | Yes (`-changed-files` flag) |
| Uncommitted changes | No (only committed diffs) | Included in diff (compares working tree) |

**GTA advantage**: GTA's branch-point detection (`branchPointOf` at `differ.go:267`) is more sophisticated. It finds the correct branch point even when the base branch has been merged into the feature branch, avoiding the merge-base pitfall that Knit's simple `git merge-base` approach suffers from.

### 3.2 Workspace Support

| Feature | Knit | GTA |
|---------|------|-----|
| `go.work` detection | Implicit via `go list -m` | Explicit via `go env GOWORK` + `modfile.ParseWork` |
| Module enumeration | `go list -m -json` output | Parses `go.work` `use` directives |
| Cross-module deps | Module-level edges only | Full package-level import graph across modules |
| Disable workspace | No | `-no-workspace` flag + `GOWORK=off` in env |
| Nested modules | Longest-path prefix match | Handled by `packages.Load` workspace mode |

**GTA advantage**: GTA's explicit `go.work` parsing gives it access to the workspace structure (which modules are included) independently of the Go toolchain. This enables features like detecting when `go.work` itself changes.

### 3.3 Dependency Graph

| Feature | Knit | GTA |
|---------|------|-----|
| Granularity | Module-level | Package-level |
| Graph type | DAG (acyclic enforced) | Directed graph (cycles tolerated via visited set) |
| Test dependencies | Not distinguished | Separate `testOnlyReverse` graph |
| External dependencies | Excluded from graph | Included in forward graph (proposed: filtered from reverse) |
| Graph library | `dominikbraun/graph` | Hand-rolled adjacency maps |
| Transitive traversal | DFS via graph library | Recursive DFS with visited marking |

### 3.4 Output and CI Integration

| Feature | Knit | GTA |
|---------|------|-----|
| Output: list | Module paths, one per line | Package import paths, space or newline separated |
| Output: JSON | `{"module":["mod1","mod2"]}` | `{"dependencies":{...},"changes":[...],"all_changes":[...]}` |
| GitHub Actions matrix | Built-in `--format github-matrix` | Not built-in (but JSON output is parseable) |
| Graphviz DOT output | `knit graph --format dot` | No |
| Built-in test runner | `knit test [--affected]` | No (outputs list for external runner) |
| Prefix filtering | `--target <module>` (single module) | `-include <prefixes>` (comma-separated, multiple) |
| Buildable-only filter | No | `-buildable-only` flag |

**Knit advantage**: Built-in `github-matrix` output and `knit test --affected` are convenient for CI. GTA requires wrapping in shell scripts.

**GTA advantage**: Richer JSON output with the full dependency map (`Dependencies`), not just a flat list. The `-include` prefix filter supports multiple prefixes.

### 3.5 Vendoring

| Feature | Knit | GTA (current) | GTA (proposed) |
|---------|------|---------------|----------------|
| Requires vendoring | No | Yes | No |
| Vendor-aware paths | No | `stripVendor()` | `stripVendor()` (backward compatible) |
| Works with module cache | Yes | Fails | Yes (graceful skip on errors) |
| Works offline | Only if cache populated | Only if vendored | Only if cache populated |

---

## 4. Dependency Change Detection

This is the critical comparison for the stated use case.

### 4.1 Scenario: External Dependency Version Bump

**Setup**: Module A imports `golang.org/x/text`. The go.mod changes `golang.org/x/text v0.3.0` to `v0.4.0`.

**Knit**:
1. `git diff` shows `go.mod` changed in module A's directory
2. File-path prefix matching marks module A as affected
3. `knit test --affected` runs `go test ./...` for ALL of module A
4. Does NOT propagate to modules that depend on A (unless `--include-deps`)
5. Does NOT analyze what changed in `x/text` -- just marks the whole module

**Knit's `--include-deps` flag**: Traverses **forward** dependencies (what A depends on), not **reverse** dependencies (what depends on A). This is backwards for the "what needs retesting" use case.

**GTA (proposed)**:
1. `git diff` shows `go.mod` changed
2. `diffGoMod()` parses old and new go.mod, identifies `golang.org/x/text` version changed
3. `LocalImportersOf(["golang.org/x/text"])` finds specific packages in module A that import `x/text`
4. Only those packages (e.g., `mymod/pkg/i18n`) are marked as changed
5. Reverse graph traversal finds packages that depend on `mymod/pkg/i18n`
6. Output: precise list of affected packages

**Winner**: GTA -- far more precise. Knit marks the entire module; GTA marks only the packages that actually import the changed dependency.

### 4.2 Scenario: Cross-Module Dependency Change in Workspace

**Setup**: Workspace with modules A, B, C. Module B imports module A. A file in module A changes.

**Knit**:
1. `git diff` shows file changed in module A's directory
2. Module A marked as affected
3. With `--include-deps`: traverses A's **forward** deps (what A depends on) -- does NOT find B
4. Without manual graph reversal, B is NOT detected as needing retesting
5. **Bug/limitation**: Knit's `--include-deps` goes the wrong direction

**GTA**:
1. `git diff` shows file changed in module A's directory
2. `PackageFromDir` resolves to specific package in A (e.g., `workspace.test/modA/pkg`)
3. Reverse graph traversal finds `workspace.test/modB/handler` imports `workspace.test/modA/pkg`
4. Continues traversal: finds anything depending on `modB/handler`
5. Output: `modA/pkg`, `modB/handler`, and all transitive dependents

**Winner**: GTA -- correct reverse dependency traversal. Knit's `--include-deps` appears to traverse forward (what the affected module depends on), which doesn't answer "what depends on the affected module."

### 4.3 Scenario: go.mod Dependency Added

**Setup**: A new `require` directive is added to go.mod.

**Knit**: Marks the containing module as affected (go.mod is a changed file). Does not analyze which packages import the new dependency.

**GTA (proposed)**: Parses go.mod diff, identifies the new module path, uses `LocalImportersOf` to find packages that import from it. Since the dependency is new, there may be new import statements in .go files too -- those would be caught by the regular file-change detection. The go.mod diff analysis acts as a safety net.

### 4.4 Scenario: go.mod Dependency Removed

**Knit**: Marks the containing module as affected.

**GTA (proposed)**: Parses go.mod diff, identifies the removed module. Finds local packages that imported from it -- those packages will likely have build errors (broken imports). They are correctly marked as affected.

### 4.5 Scenario: Replace Directive Change

**Setup**: `replace example.com/foo => ../local-foo` changed to `replace example.com/foo => ../local-foo-v2`.

**Knit**: Marks the containing module (go.mod changed). Does not analyze the replace semantics.

**GTA (proposed)**: `diffGoMod` can compare `Replace` directives. Identifies `example.com/foo` as a changed module and marks its local importers. This is semantically correct because the replacement source changed, potentially altering behavior.

---

## 5. Precision Analysis

### 5.1 Granularity Impact

Consider a module with 50 packages, where only 1 package imports a changed dependency:

| Tool | Packages marked "affected" | Tests run |
|------|---------------------------|-----------|
| Knit | All 50 (entire module) | `go test ./...` for the module |
| GTA | 1 + its dependents (say 3) | `go test` for 4 packages |

For large modules, this is a **12.5x reduction** in test scope.

### 5.2 Test-Only Dependency Precision

Consider: package `utils` is imported by `api` (production) and `worker` (test only).

When `utils` changes:

| Tool | Behavior |
|------|----------|
| Knit | Marks entire modules containing `api` and `worker` |
| GTA (`-test-transitive=true`) | Marks `api`, `worker`, and worker's production dependents |
| GTA (`-test-transitive=false`) | Marks `api` and `worker` but NOT worker's production dependents |

GTA's `testOnlyReverse` graph enables this distinction. Knit has no concept of test-only vs production dependencies.

### 5.3 False Positives Comparison

| Scenario | Knit false positives | GTA false positives |
|----------|---------------------|---------------------|
| go.mod formatting change (no version change) | Entire module | None (diff shows no version change) |
| go.sum-only change | Module (if go.sum in diff) | None (go.sum not parsed as go.mod) |
| Non-Go file change in module dir | Entire module | Only packages in that directory (via embed detection) |
| Comment-only change in .go file | Entire module | Package + dependents (same as real change) |

### 5.4 False Negatives Comparison

| Scenario | Knit misses | GTA misses |
|----------|------------|------------|
| Transitive dep change (dep-of-dep version bump, no go.mod change) | Yes | Yes (same limitation) |
| Cross-module reverse deps | Yes (`--include-deps` goes wrong direction) | No (correct reverse traversal) |
| External dep API change within same version | Yes | Yes (can't detect without downloading both versions) |

---

## 6. Strengths and Weaknesses

### 6.1 Knit Strengths

1. **Zero configuration**: No vendoring, no setup -- just works with `go.work`
2. **Built-in CI helpers**: `--format github-matrix`, `knit test --affected`
3. **Simple mental model**: "which modules changed?" is easy to reason about
4. **Small codebase**: ~1,040 lines, easy to audit and fork
5. **Fast for small workspaces**: Module-level analysis avoids loading all packages

### 6.2 Knit Weaknesses

1. **Module-level granularity**: Marks entire modules, not specific packages -- wasteful for large modules
2. **`--include-deps` goes wrong direction**: Traverses forward deps (what the affected module imports) instead of reverse deps (what imports the affected module). This is a significant bug for the "what needs retesting" use case.
3. **No go.mod semantic analysis**: A go.mod change marks the whole module but doesn't propagate to other modules that consume the changed dependency
4. **No test-only dependency tracking**: Cannot distinguish "only tests import this" from "production code imports this"
5. **Fragile error handling**: A single broken package in any module fails `go list` entirely -- no partial results
6. **Hardcoded concurrency**: 3 goroutines, not configurable
7. **Limited commands**: Only `go test` and `go fmt` -- no arbitrary command execution
8. **No non-git mode**: Requires git for change detection

### 6.3 GTA Strengths

1. **Package-level precision**: Tests only the specific packages affected, not entire modules
2. **Correct reverse dependency traversal**: Properly walks "who depends on this?" graph
3. **Test-only dependency separation**: `testOnlyReverse` graph enables precise test scoping
4. **Sophisticated branch detection**: Handles merge-base, merge commits, squash merges correctly
5. **Non-git mode**: `-changed-files` flag enables use without git
6. **Mature codebase**: 8+ years of development, extensive test suite, used at DigitalOcean scale
7. **Proposed go.mod diff**: Would provide precise dependency change tracking that Knit lacks
8. **Rich JSON output**: Full dependency map, not just a flat list

### 6.4 GTA Weaknesses

1. **Currently requires vendoring**: The proposed unvendored support would fix this
2. **No built-in test runner**: Outputs a list; user must pipe to `go test`
3. **No GitHub Actions matrix output**: Must be scripted
4. **Slower startup**: `packages.Load` is heavier than `go list` shell-out for small repos
5. **No graph visualization**: No DOT/tree output
6. **More complex codebase**: ~2,500+ lines, harder to understand at a glance

---

## 7. Gap Analysis: What Each Tool Misses

### 7.1 Neither Tool Handles

| Gap | Description |
|-----|-------------|
| Transitive external dep changes | If dep-of-dep changes but go.mod doesn't reflect it, both tools miss it |
| Build constraint changes | A file gaining/losing a `//go:build` tag could change what's built |
| CGO dependency changes | C library version changes aren't tracked |
| Generated file staleness | If a `go generate` target is stale, neither detects it |

### 7.2 Knit Misses but GTA Handles

| Gap | Description |
|-----|-------------|
| Package-level precision | Knit can't say "only this package in the module is affected" |
| Reverse dependency propagation | Knit's `--include-deps` goes forward, not reverse |
| Test-only dependency scoping | Knit runs all tests regardless of dependency type |
| Non-git mode | Knit requires git; GTA supports `-changed-files` |
| Merge commit handling | Knit's `--merge-base` is simpler; GTA handles complex merge topologies |
| go.mod semantic diff (proposed) | GTA's proposed `diffGoMod` provides precise dep change analysis |

### 7.3 GTA Misses but Knit Handles

| Gap | Description |
|-----|-------------|
| Zero-config unvendored support | Knit works without vendoring today; GTA currently requires it |
| Built-in test runner | `knit test --affected` is convenient |
| GitHub Actions matrix | `--format github-matrix` is CI-friendly |
| Graphviz visualization | `knit graph --format dot` aids debugging |

---

## 8. Recommendation

### 8.1 For the Stated Use Case

> "GTA only build/test packages that have changed, or have had a dependency change (version or otherwise)"

**GTA with the proposed unvendored support is the better tool.** The key reasons:

1. **Package-level precision** means fewer unnecessary tests
2. **Correct reverse dependency traversal** means nothing is missed when a dependency changes
3. **Proposed go.mod diff analysis** provides precise dependency change tracking that Knit entirely lacks
4. **Test-only dependency separation** allows further scoping

### 8.2 What to Borrow from Knit

While GTA is more capable, Knit has UX features worth adopting:

| Knit Feature | Recommendation for GTA |
|-------------|------------------------|
| `--format github-matrix` | Add to `cmd/gta` as output format option |
| `knit test --affected` | Consider a convenience wrapper or document `go test $(gta ...)` |
| `knit graph --format dot` | Consider adding DOT output for debugging |
| Zero-config workspace | Already implemented in our fork |

### 8.3 Priority of GTA Enhancements

1. **Implement unvendored support** (Change A: graceful external package handling) -- this removes the biggest GTA disadvantage vs Knit
2. **Implement go.mod diff analysis** (Change B) -- this gives GTA a unique advantage no other tool has
3. **Add GitHub Actions matrix output** -- matches Knit's CI convenience
4. **Add graph visualization** -- useful for debugging dependency issues

### 8.4 When to Use Knit Instead

Knit may be preferable when:
- The workspace has many small modules where module-level granularity is sufficient
- Zero configuration is more important than precision
- You want a built-in test runner (`knit test`) without scripting
- You need GitHub Actions matrix output immediately

---

## 9. References

### Source Material

| Resource | URL |
|----------|-----|
| Knit repository | https://github.com/nicolasgere/knit |
| GTA upstream repository | https://github.com/digitalocean/gta |
| GTA fork (this repo) | bts-bastion/gta |

### Design Documents (This Repo)

| Document | Path |
|----------|------|
| Go workspace support plan | `docs/go-workspace-support-plan.md` |
| Unvendored repo support plan | `docs/unvendored-repo-support-plan.md` |

### Key Source Files Analyzed

**Knit** (from GitHub):

| File | Lines | Purpose |
|------|-------|---------|
| `main.go` | ~470 | CLI, command definitions, `knit affected/test/fmt/graph` |
| `lib/analyser/parse.go` | ~200 | `ListModule`, `ListPackages`, `BuildDependencyGraph` |
| `lib/git/main.go` | ~120 | `GetChangedFiles`, `FindAffectedModuleDirs` |
| `lib/runner/runner.go` | ~90 | Concurrent command execution with semaphore |

**GTA** (this repo):

| File | Lines | Purpose |
|------|-------|---------|
| `gta.go` | ~720 | Core: `ChangedPackages`, `markedPackages`, workspace detection |
| `packager.go` | ~435 | `packages.Load`, dependency graph construction |
| `differ.go` | ~310 | Git diff, branch-point detection |
| `cmd/gta/main.go` | ~160 | CLI flags and output formatting |
| `options.go` | ~65 | Configuration options |
