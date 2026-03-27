//go:build integration
// +build integration

package gtaintegration

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/build"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bts-bastion/gta"

	"github.com/google/go-cmp/cmp"
)

const (
	repoRoot = "gtaintegration"
)

func TestMain(m *testing.M) {
	if err := testMain(m); err != nil {
		log.Println(err)
		os.Exit(1)
	}
}

func TestPackageRemoval(t *testing.T) {
	ctx := context.Background()
	if _, err := runGit(ctx, ".", "checkout", "-b", t.Name(), "master"); err != nil {
		t.Fatal(err)
	}

	// delete all go files from gofilesdeleted
	deleteGoFilesDir, err := os.Open(filepath.Clean("src/gtaintegration/gofilesdeleted"))

	if err != nil {
		t.Fatal(err)
	}
	defer deleteGoFilesDir.Close()

	names, err := deleteGoFilesDir.Readdirnames(-1)
	if err != nil {
		t.Fatal(err)
	}

	for _, fn := range names {
		if filepath.Ext(fn) == ".go" {
			err := os.Remove(filepath.Join(filepath.Clean("src/gtaintegration/gofilesdeleted"), fn))
			if err != nil {
				t.Fatal(err)
			}
		}
	}

	// fully delete deleted
	if err := os.RemoveAll(filepath.Clean("src/gtaintegration/deleted")); err != nil {
		t.Fatal(err)
	}

	// move some files to a different directory
	if _, err := runGit(ctx, ".", "mv", "src/gtaintegration/movedfrom", "src/gtaintegration/movedto"); err != nil {
		t.Fatal(err)
	}

	if testing.Verbose() {
		out, err := runGit(ctx, ".", "status", "--short")
		if err != nil {
			t.Fatal(err)
		}

		t.Logf("\n%s", out)
	}

	if _, err := runGit(ctx, ".", "commit", "-a", "-m", "delete some stuff"); err != nil {
		t.Fatal(err)
	}

	if testing.Verbose() {
		out, err := runGit(ctx, ".", "diff", "origin/master...HEAD", "--name-only", "--no-renames")
		if err != nil {
			t.Fatal(err)
		}

		t.Logf("\n%s", out)
	}
	options := []gta.Option{
		gta.SetDiffer(gta.NewGitDiffer()),
		gta.SetPrefixes("gtaintegration"),
	}

	t.Cleanup(chdir(t, filepath.Join("src", "gtaintegration")))

	gt, err := gta.New(options...)
	if err != nil {
		t.Fatalf("can't prepare gta: %v", err)
	}

	want := &gta.Packages{
		Dependencies: map[string][]gta.Package{
			"gtaintegration/deleted": []gta.Package{
				gta.Package{
					ImportPath: "gtaintegration/deletedclient",
				},
			},
			"gtaintegration/gofilesdeleted": []gta.Package{
				gta.Package{
					ImportPath: "gtaintegration/gofilesdeletedclient",
				},
			},
			"gtaintegration/movedfrom": []gta.Package{
				gta.Package{
					ImportPath: "gtaintegration/movedfromclient",
				},
			},
		},
		Changes: []gta.Package{
			gta.Package{
				ImportPath: "gtaintegration/deleted",
			},
			gta.Package{
				ImportPath: "gtaintegration/gofilesdeleted",
			},
			gta.Package{
				ImportPath: "gtaintegration/movedfrom",
			},
			gta.Package{
				ImportPath: "gtaintegration/movedto",
			},
		},
		AllChanges: []gta.Package{
			gta.Package{
				ImportPath: "gtaintegration/deleted",
			},
			gta.Package{
				ImportPath: "gtaintegration/deletedclient",
			},
			gta.Package{
				ImportPath: "gtaintegration/gofilesdeleted",
			},
			gta.Package{
				ImportPath: "gtaintegration/gofilesdeletedclient",
			},
			gta.Package{
				ImportPath: "gtaintegration/movedfrom",
			},
			gta.Package{
				ImportPath: "gtaintegration/movedfromclient",
			},
			gta.Package{
				ImportPath: "gtaintegration/movedto",
			},
		},
	}

	got, err := gt.ChangedPackages()
	if err != nil {
		t.Fatalf("err = %q; want nil", err)
	}

	if diff := cmp.Diff(mapFromPackages(t, want), mapFromPackages(t, got)); diff != "" {
		t.Errorf("(-want, +got)\n%s", diff)
	}
}

func TestPackageRemoval_AllGoFilesDeleted(t *testing.T) {
	ctx := context.Background()
	if _, err := runGit(ctx, ".", "checkout", "-b", t.Name(), "master"); err != nil {
		t.Fatal(err)
	}

	// delete all go files from gofilesdeleted
	deleteGoFilesDir, err := os.Open(filepath.Clean("src/gtaintegration/gofilesdeleted"))

	if err != nil {
		t.Fatal(err)
	}
	defer deleteGoFilesDir.Close()

	names, err := deleteGoFilesDir.Readdirnames(-1)
	if err != nil {
		t.Fatal(err)
	}

	for _, fn := range names {
		if filepath.Ext(fn) == ".go" {
			err := os.Remove(filepath.Join(filepath.Clean("src/gtaintegration/gofilesdeleted"), fn))
			if err != nil {
				t.Fatal(err)
			}
		}
	}

	if testing.Verbose() {
		out, err := runGit(ctx, ".", "status", "--short")
		if err != nil {
			t.Fatal(err)
		}

		t.Logf("\n%s", out)
	}

	if _, err := runGit(ctx, ".", "commit", "-a", "-m", "delete some stuff"); err != nil {
		t.Fatal(err)
	}

	if testing.Verbose() {
		out, err := runGit(ctx, ".", "diff", "origin/master...HEAD", "--name-only", "--no-renames")
		if err != nil {
			t.Fatal(err)
		}

		t.Logf("\n%s", out)
	}
	options := []gta.Option{
		gta.SetDiffer(gta.NewGitDiffer()),
		gta.SetPrefixes("gtaintegration"),
	}

	t.Cleanup(chdir(t, filepath.Join("src", "gtaintegration")))

	gt, err := gta.New(options...)
	if err != nil {
		t.Fatalf("can't prepare gta: %v", err)
	}

	want := &gta.Packages{
		Dependencies: map[string][]gta.Package{
			"gtaintegration/gofilesdeleted": []gta.Package{
				gta.Package{
					ImportPath: "gtaintegration/gofilesdeletedclient",
				},
			},
		},
		Changes: []gta.Package{
			gta.Package{
				ImportPath: "gtaintegration/gofilesdeleted",
			},
		},
		AllChanges: []gta.Package{
			gta.Package{
				ImportPath: "gtaintegration/gofilesdeleted",
			},
			gta.Package{
				ImportPath: "gtaintegration/gofilesdeletedclient",
			},
		},
	}

	got, err := gt.ChangedPackages()
	if err != nil {
		t.Fatalf("err = %q; want nil", err)
	}

	if diff := cmp.Diff(mapFromPackages(t, want), mapFromPackages(t, got)); diff != "" {
		t.Errorf("(-want, +got)\n%s", diff)
	}
}

func TestPackageRemoval_RemoveDirectory(t *testing.T) {
	ctx := context.Background()
	if _, err := runGit(ctx, ".", "checkout", "-b", t.Name(), "master"); err != nil {
		t.Fatal(err)
	}

	// fully delete deleted
	if err := os.RemoveAll(filepath.Clean("src/gtaintegration/deleted")); err != nil {
		t.Fatal(err)
	}

	if testing.Verbose() {
		out, err := runGit(ctx, ".", "status", "--short")
		if err != nil {
			t.Fatal(err)
		}

		t.Logf("\n%s", out)
	}

	if _, err := runGit(ctx, ".", "commit", "-a", "-m", "delete some stuff"); err != nil {
		t.Fatal(err)
	}

	if testing.Verbose() {
		out, err := runGit(ctx, ".", "diff", "origin/master...HEAD", "--name-only", "--no-renames")
		if err != nil {
			t.Fatal(err)
		}

		t.Logf("\n%s", out)
	}
	options := []gta.Option{
		gta.SetDiffer(gta.NewGitDiffer()),
		gta.SetPrefixes("gtaintegration"),
	}

	t.Cleanup(chdir(t, filepath.Join("src", "gtaintegration")))

	gt, err := gta.New(options...)
	if err != nil {
		t.Fatalf("can't prepare gta: %v", err)
	}

	want := &gta.Packages{
		Dependencies: map[string][]gta.Package{
			"gtaintegration/deleted": []gta.Package{
				gta.Package{
					ImportPath: "gtaintegration/deletedclient",
				},
			},
		},
		Changes: []gta.Package{
			gta.Package{
				ImportPath: "gtaintegration/deleted",
			},
		},
		AllChanges: []gta.Package{
			gta.Package{
				ImportPath: "gtaintegration/deleted",
			},
			gta.Package{
				ImportPath: "gtaintegration/deletedclient",
			},
		},
	}

	got, err := gt.ChangedPackages()
	if err != nil {
		t.Fatalf("err = %q; want nil", err)
	}

	if diff := cmp.Diff(mapFromPackages(t, want), mapFromPackages(t, got)); diff != "" {
		t.Errorf("(-want, +got)\n%s", diff)
	}
}

func TestPackageRemoval_MovePackage(t *testing.T) {
	ctx := context.Background()
	if _, err := runGit(ctx, ".", "checkout", "-b", t.Name(), "master"); err != nil {
		t.Fatal(err)
	}

	// move some files to a different directory
	if _, err := runGit(ctx, ".", "mv", "src/gtaintegration/movedfrom", "src/gtaintegration/movedto"); err != nil {
		t.Fatal(err)
	}

	if testing.Verbose() {
		out, err := runGit(ctx, ".", "status", "--short")
		if err != nil {
			t.Fatal(err)
		}

		t.Logf("\n%s", out)
	}

	if _, err := runGit(ctx, ".", "commit", "-a", "-m", "delete some stuff"); err != nil {
		t.Fatal(err)
	}

	if testing.Verbose() {
		out, err := runGit(ctx, ".", "diff", "origin/master...HEAD", "--name-only", "--no-renames")
		if err != nil {
			t.Fatal(err)
		}

		t.Logf("\n%s", out)
	}
	options := []gta.Option{
		gta.SetDiffer(gta.NewGitDiffer()),
		gta.SetPrefixes("gtaintegration"),
	}

	t.Cleanup(chdir(t, filepath.Join("src", "gtaintegration")))

	gt, err := gta.New(options...)
	if err != nil {
		t.Fatalf("can't prepare gta: %v", err)
	}

	want := &gta.Packages{
		Dependencies: map[string][]gta.Package{
			"gtaintegration/movedfrom": []gta.Package{
				gta.Package{
					ImportPath: "gtaintegration/movedfromclient",
				},
			},
		},
		Changes: []gta.Package{
			gta.Package{
				ImportPath: "gtaintegration/movedfrom",
			},
			gta.Package{
				ImportPath: "gtaintegration/movedto",
			},
		},
		AllChanges: []gta.Package{
			gta.Package{
				ImportPath: "gtaintegration/movedfrom",
			},
			gta.Package{
				ImportPath: "gtaintegration/movedfromclient",
			},
			gta.Package{
				ImportPath: "gtaintegration/movedto",
			},
		},
	}

	got, err := gt.ChangedPackages()
	if err != nil {
		t.Fatalf("err = %q; want nil", err)
	}

	if diff := cmp.Diff(mapFromPackages(t, want), mapFromPackages(t, got)); diff != "" {
		t.Errorf("(-want, +got)\n%s", diff)
	}
}

func TestChangesAfterMergeBaseBranch(t *testing.T) {
	ctx := context.Background()

	baseBranch := t.Name() + "-base"
	// create a base branch
	if _, err := runGit(ctx, ".", "checkout", "-b", baseBranch, "master"); err != nil {
		t.Fatal(err)
	}

	// move some files to a different directory
	if _, err := runGit(ctx, ".", "mv", "src/gtaintegration/movedfrom", "src/gtaintegration/movedto"); err != nil {
		t.Fatal(err)
	}

	if _, err := runGit(ctx, ".", "commit", "-a", "-m", "moved some stuff"); err != nil {
		t.Fatal(err)
	}

	// create the topic branch from the original branch point of the base branch
	if _, err := runGit(ctx, ".", "checkout", "-b", t.Name(), baseBranch+"^"); err != nil {
		t.Fatal(err)
	}

	// fully delete deleted
	if err := os.RemoveAll(filepath.Clean("src/gtaintegration/deleted")); err != nil {
		t.Fatal(err)
	}

	if testing.Verbose() {
		out, err := runGit(ctx, ".", "status", "--short")
		if err != nil {
			t.Fatal(err)
		}

		t.Logf("\n%s", out)
	}

	if _, err := runGit(ctx, ".", "commit", "-a", "-m", "delete some stuff"); err != nil {
		t.Fatal(err)
	}

	// merge the base branch. After this, the branch topology should look like this:
	// * (topic) merge
	// |\
	// * | delete some stuff
	// | * moved some stuff
	// | /
	// * (master) initial commit
	if _, err := runGit(ctx, ".", "merge", "--no-ff", baseBranch); err != nil {
		t.Fatal(err)
	}

	if testing.Verbose() {
		out, err := runGit(ctx, ".", "diff", baseBranch+"^...HEAD", "--name-only", "--no-renames")
		if err != nil {
			t.Fatal(err)
		}

		t.Logf("\n%s", out)
	}

	options := []gta.Option{
		gta.SetDiffer(gta.NewGitDiffer(gta.SetBaseBranch(baseBranch))),
		gta.SetPrefixes("gtaintegration"),
	}

	t.Cleanup(chdir(t, filepath.Join("src", "gtaintegration")))

	gt, err := gta.New(options...)
	if err != nil {
		t.Fatalf("can't prepare gta: %v", err)
	}

	want := &gta.Packages{
		Dependencies: map[string][]gta.Package{
			"gtaintegration/movedfrom": []gta.Package{
				gta.Package{
					ImportPath: "gtaintegration/movedfromclient",
				},
			},
			"gtaintegration/deleted": []gta.Package{
				gta.Package{
					ImportPath: "gtaintegration/deletedclient",
				},
			},
		},
		Changes: []gta.Package{
			gta.Package{
				ImportPath: "gtaintegration/deleted",
			},
			gta.Package{
				ImportPath: "gtaintegration/movedfrom",
			},
			gta.Package{
				ImportPath: "gtaintegration/movedto",
			},
		},
		AllChanges: []gta.Package{
			gta.Package{
				ImportPath: "gtaintegration/deleted",
			},
			gta.Package{
				ImportPath: "gtaintegration/deletedclient",
			},
			gta.Package{
				ImportPath: "gtaintegration/movedfrom",
			},
			gta.Package{
				ImportPath: "gtaintegration/movedfromclient",
			},
			gta.Package{
				ImportPath: "gtaintegration/movedto",
			},
		},
	}

	got, err := gt.ChangedPackages()
	if err != nil {
		t.Fatalf("err = %q; want nil", err)
	}

	if diff := cmp.Diff(mapFromPackages(t, want), mapFromPackages(t, got)); diff != "" {
		t.Errorf("(-want, +got)\n%s", diff)
	}
}

func TestPackageRemoval_MovePackage_NonMasterBranch(t *testing.T) {
	ctx := context.Background()
	if _, err := runGit(ctx, ".", "checkout", "-b", t.Name(), "master"); err != nil {
		t.Fatal(err)
	}

	// move some files to a different directory
	if _, err := runGit(ctx, ".", "mv", "src/gtaintegration/movedfrom", "src/gtaintegration/movedto"); err != nil {
		t.Fatal(err)
	}

	if testing.Verbose() {
		out, err := runGit(ctx, ".", "status", "--short")
		if err != nil {
			t.Fatal(err)
		}

		t.Logf("\n%s", out)
	}

	if _, err := runGit(ctx, ".", "commit", "-a", "-m", "delete some stuff"); err != nil {
		t.Fatal(err)
	}

	if testing.Verbose() {
		out, err := runGit(ctx, ".", "diff", "origin/feature-branch...HEAD", "--name-only", "--no-renames")
		if err != nil {
			t.Fatal(err)
		}

		t.Logf("\n%s", out)
	}
	options := []gta.Option{
		gta.SetDiffer(gta.NewGitDiffer(gta.SetBaseBranch("origin/feature-branch"))),
		gta.SetPrefixes("gtaintegration"),
	}

	t.Cleanup(chdir(t, filepath.Join("src", "gtaintegration")))

	gt, err := gta.New(options...)
	if err != nil {
		t.Fatalf("can't prepare gta: %v", err)
	}

	want := &gta.Packages{
		Dependencies: map[string][]gta.Package{
			"gtaintegration/movedfrom": []gta.Package{
				gta.Package{
					ImportPath: "gtaintegration/movedfromclient",
				},
			},
		},
		Changes: []gta.Package{
			gta.Package{
				ImportPath: "gtaintegration/movedfrom",
			},
			gta.Package{
				ImportPath: "gtaintegration/movedto",
			},
		},
		AllChanges: []gta.Package{
			gta.Package{
				ImportPath: "gtaintegration/movedfrom",
			},
			gta.Package{
				ImportPath: "gtaintegration/movedfromclient",
			},
			gta.Package{
				ImportPath: "gtaintegration/movedto",
			},
		},
	}

	got, err := gt.ChangedPackages()
	if err != nil {
		t.Fatalf("err = %q; want nil", err)
	}

	if diff := cmp.Diff(mapFromPackages(t, want), mapFromPackages(t, got)); diff != "" {
		t.Errorf("(-want, +got)\n%s", diff)
	}
}

func TestNonPackageRemoval(t *testing.T) {
	ctx := context.Background()
	if _, err := runGit(ctx, ".", "checkout", "-b", t.Name(), "master"); err != nil {
		t.Fatal(err)
	}

	// fully delete nogodeleted
	if err := os.RemoveAll(filepath.Clean("src/gtaintegration/nogodeleted")); err != nil {
		t.Fatal(err)
	}

	if testing.Verbose() {
		out, err := runGit(ctx, ".", "status", "--short")
		if err != nil {
			t.Fatal(err)
		}

		t.Logf("\n%s", out)
	}

	if _, err := runGit(ctx, ".", "commit", "-a", "-m", "delete some stuff"); err != nil {
		t.Fatal(err)
	}

	if testing.Verbose() {
		out, err := runGit(ctx, ".", "diff", "origin/master...HEAD", "--name-only", "--no-renames")
		if err != nil {
			t.Fatal(err)
		}

		t.Logf("\n%s", out)
	}
	options := []gta.Option{
		gta.SetDiffer(gta.NewGitDiffer()),
		gta.SetPrefixes("gtaintegration"),
	}

	gt, err := gta.New(options...)
	if err != nil {
		t.Fatalf("can't prepare gta: %v", err)
	}

	want := &gta.Packages{
		Dependencies: map[string][]gta.Package{},
		Changes:      []gta.Package{},
		AllChanges:   []gta.Package{},
	}

	got, err := gt.ChangedPackages()
	if err != nil {
		t.Fatalf("err = %q; want nil", err)
	}

	if diff := cmp.Diff(mapFromPackages(t, want), mapFromPackages(t, got)); diff != "" {
		t.Errorf("(-want, +got)\n%s", diff)
	}
}
func testMain(m *testing.M) error {
	flag.Parse()

	// copy all of testdata to a temporary directory, because we're going to
	// mutate it.
	wd := prepareTemp()

	// change to the temporary directory
	err := os.Chdir(wd)
	if err != nil {
		return err
	}
	defer os.RemoveAll(wd)

	// configure the repository.
	err = createRepo(repoRoot)
	if err != nil {
		return err
	}

	// change to the repository for the remainder of the tests
	err = os.Chdir(repoRoot)
	if err != nil {
		return err
	}

	wd, err = os.Getwd()
	if err != nil {
		return err
	}

	// TODO(bc): don't set GOPATH; it's unsupported as of Go 1.17
	build.Default.GOPATH = wd
	os.Setenv("GOPATH", wd)

	if i := m.Run(); i != 0 {
		return errors.New("failed")
	}

	return nil
}

func createRepo(path string) error {
	if testing.Verbose() {
		log.Println("creating repo in " + path)
	}

	ctx := context.Background()
	// git init
	if _, err := runGit(ctx, path, "init", "-b", "master"); err != nil {
		return err
	}

	if _, err := runGit(ctx, path, "add", "src"); err != nil {
		return err
	}

	if _, err := runGit(ctx, path, "commit", "-m", "initial commit"); err != nil {
		return err
	}

	// create an origin/master branch from master to simulate a remote.
	if _, err := runGit(ctx, path, "branch", "origin/master"); err != nil {
		return err
	}

	// create an origin/feature-branch branch from master to simulate a non-master remote.
	if _, err := runGit(ctx, path, "branch", "origin/feature-branch"); err != nil {
		return err
	}

	return nil
}

func runGit(ctx context.Context, wd string, args ...string) (string, error) {
	args = append([]string{"-c", "user.email=gtaintegration@example.com", "-c", "user.name=gtaintegration test"}, args...)

	cmd := exec.CommandContext(ctx, "git", args...)
	wd = abs(wd)

	cmd.Dir = wd

	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", errors.New(string(out))
	}
	return string(out), nil
}

func abs(path string) string {
	path, err := filepath.Abs(path)
	if err != nil {
		panic(err)
	}

	return path
}

func prepareTemp() string {
	d, err := os.MkdirTemp("", "gta-integration-tests")
	if err != nil {
		panic(err)
	}

	err = filepath.Walk("testdata", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		dstPath := filepath.Join(d, strings.TrimPrefix(path, "testdata/"))
		if info.IsDir() {
			err := os.Mkdir(dstPath, info.Mode())
			if err != nil {
				return fmt.Errorf("could not create directory (%s): %w", dstPath, err)
			}
			return nil
		}

		src, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("could not open file (%s): %w", path, err)
		}
		defer src.Close()

		dst, err := os.Create(dstPath)
		if err != nil {
			return fmt.Errorf("could not create file (%s): %w", dstPath, err)
		}
		defer dst.Close()

		_, err = io.Copy(dst, src)
		if err != nil {
			return fmt.Errorf("could not copy file (%s): %w", path, err)
		}

		err = os.Chmod(dst.Name(), info.Mode())
		if err != nil {
			return fmt.Errorf("could not set file permission (%s): %w", dst.Name(), err)
		}
		return nil
	})

	if err != nil {
		os.RemoveAll(d)
		panic(err)
	}

	return d
}

// setenv sets an environment variable, name, to value and returns a function
// to restore the environment variable to its former value.
func setEnv(t *testing.T, name, value string) func() {
	t.Helper()

	orig, ok := os.LookupEnv(name)

	if err := os.Setenv(name, value); err != nil {
		t.Fatal(err)
	}

	return func() {
		if !ok {
			if err := os.Unsetenv(name); err != nil {
				t.Fatal(err)
			}

			return
		}

		if err := os.Setenv(name, orig); err != nil {
			t.Fatal(err)
		}
	}
}

func mapFromPackages(t *testing.T, pkg *gta.Packages) map[string]interface{} {
	t.Helper()

	m := make(map[string]interface{})

	b, err := json.Marshal(pkg)
	if err != nil {
		t.Fatal(err)
	}

	err = json.Unmarshal(b, &m)
	if err != nil {
		t.Fatal(err)
	}

	return m
}

func chdir(t *testing.T, dir string) func() {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	os.Chdir(dir)
	return func() {
		os.Chdir(wd)
	}
}
