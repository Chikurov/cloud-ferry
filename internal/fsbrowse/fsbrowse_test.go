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

func TestProbeDir(t *testing.T) {
	root := t.TempDir()
	dest := filepath.Join(root, "dest")
	mkdir(t, filepath.Join(dest, "полная"))
	writeFile(t, filepath.Join(dest, "полная", "a.txt"))
	writeFile(t, filepath.Join(dest, "полная", ".hidden"))
	mkdir(t, filepath.Join(dest, "полная", "вложенная"))
	writeFile(t, filepath.Join(dest, "полная", "вложенная", "deep.txt"))
	mkdir(t, filepath.Join(dest, "пустая"))
	mkdir(t, filepath.Join(dest, "родитель", "ребёнок"))
	writeFile(t, filepath.Join(dest, "файл.txt"))

	outside := t.TempDir()
	mkdir(t, filepath.Join(outside, "снаружи"))
	if err := os.Symlink(outside, filepath.Join(dest, "наружу")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	if err := os.Symlink(filepath.Join(dest, "полная"), filepath.Join(dest, "внутрь")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	cases := []struct {
		name    string
		folder  string
		exists  bool
		entries int
	}{
		{"folder with entries", "полная", true, 3},
		{"empty folder", "пустая", true, 0},
		{"missing folder", "нет", false, 0},
		{"nested folder", "родитель/ребёнок", true, 0},
		{"missing nested folder", "родитель/нет", false, 0},
		{"file instead of folder", "файл.txt", false, 0},
		{"symlink outside", "наружу", false, 0},
		{"symlink inside root", "внутрь", false, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			state, err := ProbeDir([]string{root}, dest, tc.folder)
			if err != nil {
				t.Fatalf("probe: %v", err)
			}
			if state.Exists != tc.exists || state.Entries != tc.entries {
				t.Fatalf("state = %+v, want exists=%v entries=%d", state, tc.exists, tc.entries)
			}
		})
	}

	if _, err := os.Stat(filepath.Join(dest, "нет")); !os.IsNotExist(err) {
		t.Fatalf("probe created the missing folder: %v", err)
	}
}

func TestProbeDirMissingDestination(t *testing.T) {
	root := t.TempDir()
	dest := filepath.Join(root, "new", "dest")

	state, err := ProbeDir([]string{root}, dest, "Фото")
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if state.Exists || state.Entries != 0 {
		t.Fatalf("state = %+v, want not existing", state)
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatalf("probe created the destination: %v", err)
	}
}

func TestProbeDirRejectsInvalidName(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"", "..", "../x", "a/../../b", "/x", "bad\x00name"} {
		if _, err := ProbeDir([]string{root}, root, name); !errors.Is(err, ErrInvalidPath) {
			t.Fatalf("%q: got %v, want ErrInvalidPath", name, err)
		}
	}
}

func TestProbeDirRejectsOutsideRoots(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if _, err := ProbeDir([]string{root}, filepath.Join(outside, "dest"), "Фото"); !errors.Is(err, ErrOutsideRoots) {
		t.Fatalf("expected ErrOutsideRoots, got %v", err)
	}
}

func TestProbeDirRejectsUnsafeDestination(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "файл.txt"))
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	if _, err := ProbeDir([]string{root}, filepath.Join(root, "файл.txt"), "Фото"); !errors.Is(err, ErrNotDir) {
		t.Fatalf("file destination: got %v, want ErrNotDir", err)
	}
	if _, err := ProbeDir([]string{root}, filepath.Join(root, "escape"), "Фото"); !errors.Is(err, ErrSymlink) {
		t.Fatalf("symlink destination: got %v, want ErrSymlink", err)
	}
}

func TestProbeDirUnmountedRoot(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "not-mounted")
	state, err := ProbeDir([]string{missing}, filepath.Join(missing, "dest"), "Фото")
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if state.Exists {
		t.Fatalf("state = %+v, want not existing", state)
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

func TestMkdirCreatesOneDirectory(t *testing.T) {
	root := t.TempDir()
	created := filepath.Join(root, "фото")

	got, err := Mkdir([]string{root}, created)
	if err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if got != created {
		t.Fatalf("path = %q, want %q", got, created)
	}
	info, err := os.Stat(created)
	if err != nil || !info.IsDir() {
		t.Fatalf("created dir: info=%v err=%v", info, err)
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read root: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "фото" {
		t.Fatalf("root entries = %v", entries)
	}
}

func TestMkdirNestedUnderExistingParent(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "one")
	mkdir(t, parent)

	created := filepath.Join(parent, "two")
	if _, err := Mkdir([]string{root}, created); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}
	if info, err := os.Stat(created); err != nil || !info.IsDir() {
		t.Fatalf("created dir: info=%v err=%v", info, err)
	}
}

func TestMkdirOnlyCreatesLastComponent(t *testing.T) {
	root := t.TempDir()
	missing := filepath.Join(root, "missing", "child")

	if _, err := Mkdir([]string{root}, missing); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "missing")); !os.IsNotExist(err) {
		t.Fatalf("parent was created: %v", err)
	}
}

func TestMkdirExistingPath(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "there")
	mkdir(t, dir)

	if _, err := Mkdir([]string{root}, dir); !errors.Is(err, ErrExists) {
		t.Fatalf("expected ErrExists, got %v", err)
	}

	if _, err := Mkdir([]string{root}, root); !errors.Is(err, ErrExists) {
		t.Fatalf("root itself: expected ErrExists, got %v", err)
	}

	file := filepath.Join(root, "note.txt")
	writeFile(t, file)
	if _, err := Mkdir([]string{root}, file); !errors.Is(err, ErrNotDir) {
		t.Fatalf("file: expected ErrNotDir, got %v", err)
	}
	if _, err := Mkdir([]string{root}, filepath.Join(file, "sub")); !errors.Is(err, ErrNotDir) {
		t.Fatalf("file component: expected ErrNotDir, got %v", err)
	}
}

func TestMkdirRejectsSymlinkParent(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	if _, err := Mkdir([]string{root}, filepath.Join(root, "escape", "child")); !errors.Is(err, ErrSymlink) {
		t.Fatalf("expected ErrSymlink, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "child")); !os.IsNotExist(err) {
		t.Fatalf("created outside the root: %v", err)
	}

	// A symlink as the target is not followed either.
	if err := os.Symlink(outside, filepath.Join(root, "target")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	if _, err := Mkdir([]string{root}, filepath.Join(root, "target")); !errors.Is(err, ErrSymlink) {
		t.Fatalf("expected ErrSymlink for target, got %v", err)
	}
}

func TestMkdirRejectsUnsafePaths(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()

	cases := []struct {
		name      string
		requested string
		want      error
	}{
		{"empty", "", ErrInvalidPath},
		{"relative", "downloads/photo", ErrInvalidPath},
		{"nul byte", root + "/bad\x00name", ErrInvalidPath},
		{"outside", filepath.Join(outside, "photo"), ErrOutsideRoots},
		{"parent traversal", root + "/../outside", ErrOutsideRoots},
		{"root traversal", "/etc/cloud-ferry", ErrOutsideRoots},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Mkdir([]string{root}, tc.requested); !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
}

func TestMkdirUnmountedRoot(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "not-mounted")
	if _, err := Mkdir([]string{missing}, filepath.Join(missing, "new")); !errors.Is(err, ErrRootUnavailable) {
		t.Fatalf("expected ErrRootUnavailable, got %v", err)
	}
}

func TestMkdirMatchesSecondRoot(t *testing.T) {
	first := t.TempDir()
	second := t.TempDir()
	created := filepath.Join(second, "inner")

	got, err := Mkdir([]string{first, second}, created)
	if err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if got != created {
		t.Fatalf("path = %q, want %q", got, created)
	}
}
