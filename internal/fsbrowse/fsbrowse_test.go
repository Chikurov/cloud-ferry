package fsbrowse

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func mkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}

func writeFile(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestBrowseListsOnlyRealDirectoriesSorted(t *testing.T) {
	root := t.TempDir()
	mkdir(t, filepath.Join(root, "Фото"))
	mkdir(t, filepath.Join(root, "Альбомы"))
	mkdir(t, filepath.Join(root, "video"))
	writeFile(t, filepath.Join(root, "note.txt"))

	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "outside-link")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	if err := os.Symlink(filepath.Join(root, "Фото"), filepath.Join(root, "inside-link")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	listing, err := Browse([]string{root}, root)
	if err != nil {
		t.Fatalf("browse: %v", err)
	}
	if listing.Root != root || listing.Path != root {
		t.Fatalf("root/path = %q/%q", listing.Root, listing.Path)
	}
	if listing.Parent != nil {
		t.Fatalf("parent = %q, want nil", *listing.Parent)
	}
	names := make([]string, 0, len(listing.Dirs))
	for _, entry := range listing.Dirs {
		names = append(names, entry.Name)
		if entry.Path != filepath.Join(root, entry.Name) {
			t.Fatalf("entry path = %q", entry.Path)
		}
	}
	want := []string{"video", "Альбомы", "Фото"}
	if fmt.Sprint(names) != fmt.Sprint(want) {
		t.Fatalf("dirs = %v, want %v (case-insensitive sort)", names, want)
	}
	if listing.Free == 0 || listing.Total == 0 {
		t.Fatalf("free/total = %d/%d", listing.Free, listing.Total)
	}
	if listing.Truncated {
		t.Fatal("truncated = true")
	}
}

func TestBrowseNestedPathSetsParent(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "one", "two")
	mkdir(t, nested)
	mkdir(t, filepath.Join(nested, "child"))

	listing, err := Browse([]string{root}, nested)
	if err != nil {
		t.Fatalf("browse: %v", err)
	}
	if listing.Path != nested || listing.Root != root {
		t.Fatalf("path/root = %q/%q", listing.Path, listing.Root)
	}
	if listing.Parent == nil || *listing.Parent != filepath.Join(root, "one") {
		t.Fatalf("parent = %v", listing.Parent)
	}
	if len(listing.Dirs) != 1 || listing.Dirs[0].Name != "child" {
		t.Fatalf("dirs = %+v", listing.Dirs)
	}
}

func TestBrowseRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(root, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	if _, err := Browse([]string{root}, link); !errors.Is(err, ErrSymlink) {
		t.Fatalf("expected ErrSymlink, got %v", err)
	}
	if _, err := Browse([]string{root}, filepath.Join(link, "nested")); !errors.Is(err, ErrSymlink) {
		t.Fatalf("expected ErrSymlink for nested, got %v", err)
	}
}

func TestBrowseRejectsSymlinkInsideRoot(t *testing.T) {
	root := t.TempDir()
	mkdir(t, filepath.Join(root, "real"))
	link := filepath.Join(root, "inside")
	if err := os.Symlink(filepath.Join(root, "real"), link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	if _, err := Browse([]string{root}, link); !errors.Is(err, ErrSymlink) {
		t.Fatalf("expected ErrSymlink, got %v", err)
	}
}

func TestBrowseRejectsTraversalAndRelativePaths(t *testing.T) {
	root := t.TempDir()
	cases := []struct {
		name string
		path string
	}{
		{"parent", root + "/.."},
		{"nested parent", root + "/sub/../../outside"},
		{"relative", "downloads/photo"},
		{"nul byte", root + "/bad\x00name"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Browse([]string{root}, tc.path)
			if err == nil {
				t.Fatal("expected error")
			}
			if !errors.Is(err, ErrOutsideRoots) && !errors.Is(err, ErrInvalidPath) {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestBrowseRejectsFileAndMissingPath(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "note.txt")
	writeFile(t, file)

	if _, err := Browse([]string{root}, file); !errors.Is(err, ErrNotDir) {
		t.Fatalf("expected ErrNotDir, got %v", err)
	}
	if _, err := Browse([]string{root}, filepath.Join(root, "missing")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestBrowseUnmountedRoot(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "not-mounted")
	if _, err := Browse([]string{missing}, missing); !errors.Is(err, ErrRootUnavailable) {
		t.Fatalf("expected ErrRootUnavailable, got %v", err)
	}
}

func TestBrowseLimitsAndTruncates(t *testing.T) {
	root := t.TempDir()
	for i := 0; i <= MaxDirs+2; i++ {
		mkdir(t, filepath.Join(root, fmt.Sprintf("dir-%03d", i)))
	}

	listing, err := Browse([]string{root}, root)
	if err != nil {
		t.Fatalf("browse: %v", err)
	}
	if len(listing.Dirs) != MaxDirs {
		t.Fatalf("got %d dirs, want %d", len(listing.Dirs), MaxDirs)
	}
	if !listing.Truncated {
		t.Fatal("truncated = false")
	}
	if listing.Dirs[0].Name != "dir-000" || listing.Dirs[len(listing.Dirs)-1].Name != fmt.Sprintf("dir-%03d", MaxDirs-1) {
		t.Fatalf("unexpected order: first %q last %q", listing.Dirs[0].Name, listing.Dirs[len(listing.Dirs)-1].Name)
	}
}

func TestBrowseMatchesSecondRoot(t *testing.T) {
	first := t.TempDir()
	second := t.TempDir()
	mkdir(t, filepath.Join(second, "inner"))

	listing, err := Browse([]string{first, second}, filepath.Join(second, "inner"))
	if err != nil {
		t.Fatalf("browse: %v", err)
	}
	if listing.Root != second {
		t.Fatalf("root = %q, want %q", listing.Root, second)
	}
}

func TestBrowseOutsideAllRoots(t *testing.T) {
	root := t.TempDir()
	if _, err := Browse([]string{root}, "/etc"); !errors.Is(err, ErrOutsideRoots) {
		t.Fatalf("expected ErrOutsideRoots, got %v", err)
	}
}

func TestValidateDestAcceptsMissingSubdirectory(t *testing.T) {
	root := t.TempDir()
	dest := filepath.Join(root, "new", "sub")
	cleaned, err := ValidateDest([]string{root}, dest)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if cleaned != dest {
		t.Fatalf("cleaned = %q, want %q", cleaned, dest)
	}
}

func TestValidateDestRejectsUnsafePaths(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	writeFile(t, filepath.Join(root, "note.txt"))

	cases := []struct {
		name string
		dest string
		want error
	}{
		{"outside roots", filepath.Join(outside, "x"), ErrOutsideRoots},
		{"parent", root + "/../x", ErrOutsideRoots},
		{"relative", "downloads/x", ErrInvalidPath},
		{"symlink dir", filepath.Join(root, "escape"), ErrSymlink},
		{"symlink component", filepath.Join(root, "escape", "sub"), ErrSymlink},
		{"file as dest", filepath.Join(root, "note.txt"), ErrNotDir},
		{"file as component", filepath.Join(root, "note.txt", "sub"), ErrNotDir},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ValidateDest([]string{root}, tc.dest); !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
}

func TestValidateDestAllowsUnmountedRoot(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "not-mounted")
	cleaned, err := ValidateDest([]string{missing}, filepath.Join(missing, "x"))
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if cleaned != filepath.Join(missing, "x") {
		t.Fatalf("cleaned = %q", cleaned)
	}
}

func TestValidateDestNormalizesPath(t *testing.T) {
	root := t.TempDir()
	mkdir(t, filepath.Join(root, "sub"))
	cleaned, err := ValidateDest([]string{root}, root+"/sub/./")
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if cleaned != filepath.Join(root, "sub") {
		t.Fatalf("cleaned = %q", cleaned)
	}
}

func TestMatchRootSupportsFilesystemRoot(t *testing.T) {
	rootPath, rel, err := matchRoot([]string{"/"}, "/tmp/example")
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if rootPath != "/" || rel != "tmp/example" {
		t.Fatalf("root/rel = %q/%q", rootPath, rel)
	}
}
