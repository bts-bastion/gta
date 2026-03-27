package gta

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestWorkspaceRoots(t *testing.T) {
	// Save and restore the working directory.
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(origDir) })

	wsDir, err := filepath.Abs(filepath.Join("testdata", "workspacetest"))
	if err != nil {
		t.Fatal(err)
	}

	// Change to workspace directory so go env GOWORK finds the go.work file.
	if err := os.Chdir(wsDir); err != nil {
		t.Fatal(err)
	}

	roots, err := workspaceroots()
	if err != nil {
		t.Fatalf("workspaceroots() error: %v", err)
	}

	if roots == nil {
		t.Fatal("workspaceroots() returned nil; expected workspace roots")
	}

	// We expect 3 roots: modA, modB, modC (modD is not in go.work).
	if len(roots) != 3 {
		t.Fatalf("expected 3 roots, got %d: %v", len(roots), roots)
	}

	sort.Strings(roots)
	for i, want := range []string{"modA", "modB", "modC"} {
		wantSuffix := filepath.Join("workspacetest", want)
		if !containsSuffix(roots[i], wantSuffix) {
			t.Errorf("roots[%d] = %q; want suffix %q", i, roots[i], wantSuffix)
		}
	}
}

func TestWorkspaceRoots_NotInWorkspace(t *testing.T) {
	// When not in a workspace directory, workspaceroots should return nil.
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(origDir) })

	// Use a temp dir that has no go.work.
	tmpDir := t.TempDir()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}

	// Force GOWORK=off to ensure we're not in workspace mode.
	t.Setenv("GOWORK", "off")

	roots, err := workspaceroots()
	if err != nil {
		t.Fatalf("workspaceroots() error: %v", err)
	}

	if roots != nil {
		t.Fatalf("expected nil roots outside workspace, got %v", roots)
	}
}

func TestToplevel_Workspace(t *testing.T) {
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(origDir) })

	wsDir, err := filepath.Abs(filepath.Join("testdata", "workspacetest"))
	if err != nil {
		t.Fatal(err)
	}

	if err := os.Chdir(wsDir); err != nil {
		t.Fatal(err)
	}

	roots, err := toplevel(false)
	if err != nil {
		t.Fatalf("toplevel(false) error: %v", err)
	}

	if len(roots) != 3 {
		t.Fatalf("expected 3 roots, got %d: %v", len(roots), roots)
	}
}

func TestToplevel_WorkspaceDisabled(t *testing.T) {
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(origDir) })

	wsDir, err := filepath.Abs(filepath.Join("testdata", "workspacetest", "modA"))
	if err != nil {
		t.Fatal(err)
	}

	if err := os.Chdir(wsDir); err != nil {
		t.Fatal(err)
	}

	roots, err := toplevel(true)
	if err != nil {
		t.Fatalf("toplevel(true) error: %v", err)
	}

	// With workspace disabled, should return only the single module root.
	if len(roots) != 1 {
		t.Fatalf("expected 1 root with workspace disabled, got %d: %v", len(roots), roots)
	}

	if !containsSuffix(roots[0], "modA") {
		t.Errorf("root = %q; want suffix 'modA'", roots[0])
	}
}

func TestChangedPackages_WorkspaceCrossModule(t *testing.T) {
	// Test that changing a package in modB causes dependents in modA and
	// transitively modC to be detected.
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(origDir) })

	wsDir, err := filepath.Abs(filepath.Join("testdata", "workspacetest"))
	if err != nil {
		t.Fatal(err)
	}

	if err := os.Chdir(wsDir); err != nil {
		t.Fatal(err)
	}

	// Build the differ: modB/pkg/b.go changed.
	bPkgDir := filepath.Join(wsDir, "modB", "pkg")
	difr := &testDiffer{
		diff: map[string]Directory{
			bPkgDir: {Exists: true, Files: []string{"b.go"}},
		},
	}

	gta, err := New(SetDiffer(difr))
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	pkgs, err := gta.ChangedPackages()
	if err != nil {
		t.Fatalf("ChangedPackages() error: %v", err)
	}

	// Expect: workspace.test/modB/pkg changed, and its dependents
	// workspace.test/modA/pkg and workspace.test/modC/pkg should also be
	// marked.
	var gotPaths []string
	for _, pkg := range pkgs.AllChanges {
		gotPaths = append(gotPaths, pkg.ImportPath)
	}
	sort.Strings(gotPaths)

	wantPaths := []string{
		"workspace.test/modA/pkg",
		"workspace.test/modB/pkg",
		"workspace.test/modC/pkg",
	}

	if diff := cmp.Diff(wantPaths, gotPaths); diff != "" {
		t.Errorf("AllChanges import paths (-want +got):\n%s", diff)
	}

	// Verify the direct change.
	var changePaths []string
	for _, pkg := range pkgs.Changes {
		changePaths = append(changePaths, pkg.ImportPath)
	}
	if len(changePaths) != 1 || changePaths[0] != "workspace.test/modB/pkg" {
		t.Errorf("Changes = %v; want [workspace.test/modB/pkg]", changePaths)
	}
}

func TestChangedPackages_WorkspaceIsolatedModule(t *testing.T) {
	// Test that changing a package in a module with no dependents only
	// reports that one package.
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(origDir) })

	wsDir, err := filepath.Abs(filepath.Join("testdata", "workspacetest"))
	if err != nil {
		t.Fatal(err)
	}

	if err := os.Chdir(wsDir); err != nil {
		t.Fatal(err)
	}

	// modC/pkg has no dependents within the workspace.
	cPkgDir := filepath.Join(wsDir, "modC", "pkg")
	difr := &testDiffer{
		diff: map[string]Directory{
			cPkgDir: {Exists: true, Files: []string{"c.go"}},
		},
	}

	gta, err := New(SetDiffer(difr))
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	pkgs, err := gta.ChangedPackages()
	if err != nil {
		t.Fatalf("ChangedPackages() error: %v", err)
	}

	var gotPaths []string
	for _, pkg := range pkgs.AllChanges {
		gotPaths = append(gotPaths, pkg.ImportPath)
	}

	wantPaths := []string{"workspace.test/modC/pkg"}
	if diff := cmp.Diff(wantPaths, gotPaths); diff != "" {
		t.Errorf("AllChanges import paths (-want +got):\n%s", diff)
	}
}

func TestChangedPackages_GoWorkFileChanged(t *testing.T) {
	// When go.work itself is changed, all packages should be marked.
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(origDir) })

	wsDir, err := filepath.Abs(filepath.Join("testdata", "workspacetest"))
	if err != nil {
		t.Fatal(err)
	}

	if err := os.Chdir(wsDir); err != nil {
		t.Fatal(err)
	}

	// go.work changed.
	difr := &testDiffer{
		diff: map[string]Directory{
			wsDir: {Exists: true, Files: []string{"go.work"}},
		},
	}

	gta, err := New(SetDiffer(difr), SetPrefixes("workspace.test/"))
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	pkgs, err := gta.ChangedPackages()
	if err != nil {
		t.Fatalf("ChangedPackages() error: %v", err)
	}

	// When go.work changes, all workspace packages should be in AllChanges.
	if len(pkgs.AllChanges) < 3 {
		var paths []string
		for _, p := range pkgs.AllChanges {
			paths = append(paths, p.ImportPath)
		}
		t.Errorf("expected at least 3 changed packages when go.work changes, got %d: %v", len(pkgs.AllChanges), paths)
	}
}

func TestSetDisableWorkspace(t *testing.T) {
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(origDir) })

	wsDir, err := filepath.Abs(filepath.Join("testdata", "workspacetest", "modA"))
	if err != nil {
		t.Fatal(err)
	}

	if err := os.Chdir(wsDir); err != nil {
		t.Fatal(err)
	}

	// Fake differ showing a change in modA/pkg.
	aPkgDir := filepath.Join(wsDir, "pkg")
	difr := &testDiffer{
		diff: map[string]Directory{
			aPkgDir: {Exists: true, Files: []string{"a.go"}},
		},
	}

	// With workspace disabled, only modA packages should be loaded.
	gta, err := New(SetDiffer(difr), SetDisableWorkspace(true))
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	pkgs, err := gta.ChangedPackages()
	if err != nil {
		t.Fatalf("ChangedPackages() error: %v", err)
	}

	// With workspace disabled, the packager only loads modA's packages.
	// modC (which depends on modA) should NOT appear because it's in a
	// different module.
	for _, pkg := range pkgs.AllChanges {
		if pkg.ImportPath == "workspace.test/modC/pkg" {
			t.Error("modC/pkg should not appear when workspace is disabled")
		}
	}
}

func containsSuffix(s, suffix string) bool {
	return len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix
}
