// Package fsbrowse lists and creates destination directories without leaving
// the configured roots. Every filesystem access after validation goes through
// os.Root, so neither ".." nor a symlink can point outside a root.
package fsbrowse

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"

	"cloudferry/internal/disk"
)

// MaxDirs is the maximum number of directories one listing returns.
const MaxDirs = 500

// maxScanned bounds the directory entries inspected per listing, so a folder
// with millions of files cannot turn one request into unbounded work.
const maxScanned = 4096

// maxPathLen bounds a requested path before it reaches the filesystem.
const maxPathLen = 4096

// Sentinel errors. The HTTP layer maps them to status codes and short Russian
// messages; the filesystem details stay in the wrapped error.
var (
	ErrInvalidPath     = errors.New("invalid path")
	ErrOutsideRoots    = errors.New("path outside allowed roots")
	ErrRootUnavailable = errors.New("destination root is not available")
	ErrNotFound        = errors.New("path not found")
	ErrNotDir          = errors.New("path is not a directory")
	ErrSymlink         = errors.New("path uses a symlink")
	ErrExists          = errors.New("path already exists")
)

// Entry is one subdirectory of a listing. Path is absolute and includes the
// configured root.
type Entry struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// Listing is one directory level below a configured root.
type Listing struct {
	Root      string
	Path      string
	Parent    *string
	Dirs      []Entry
	Free      uint64
	Total     uint64
	Truncated bool
}

// Browse lists the directories directly inside requested, which must lie under
// one of roots. Only real directories are returned: symlinks are neither
// followed nor listed.
func Browse(roots []string, requested string) (Listing, error) {
	resolved, err := resolve(roots, requested, false)
	if err != nil {
		return Listing{}, err
	}
	defer resolved.close()

	dir, err := resolved.root.Open(relOrDot(resolved.rel))
	if err != nil {
		return Listing{}, pathError(err)
	}
	defer dir.Close()

	info, err := dir.Stat()
	if err != nil {
		return Listing{}, pathError(err)
	}
	if !info.IsDir() {
		return Listing{}, ErrNotDir
	}

	dirs, truncated, err := readDirs(dir, resolved.absolute)
	if err != nil {
		return Listing{}, err
	}
	free, total, err := disk.Usage(resolved.absolute)
	if err != nil {
		return Listing{}, fmt.Errorf("disk usage %s: %w", resolved.absolute, err)
	}

	listing := Listing{
		Root:      resolved.rootPath,
		Path:      resolved.absolute,
		Parent:    parentPath(resolved.rootPath, resolved.rel),
		Dirs:      dirs,
		Free:      free,
		Total:     total,
		Truncated: truncated,
	}
	return listing, nil
}

// ValidateDest checks a destination path the same way Browse does. It returns
// the cleaned absolute path, or an error when the path is malformed, outside
// every root, or reaches its root through a symlink. A root that is not mounted
// goes through the lexical check only: os.Root cannot be opened, and browse
// already reports that root as unavailable.
func ValidateDest(roots []string, dest string) (string, error) {
	resolved, err := resolve(roots, dest, true)
	if err != nil {
		return "", err
	}
	resolved.close()
	return resolved.absolute, nil
}

// DirState is what ProbeDir found at one path.
type DirState struct {
	Exists  bool
	Entries int
}

// ProbeDir reports whether name below dest is an existing directory and how
// many entries it holds at its top level. dest must be inside one of roots;
// name is a path relative to dest. A missing path, a file and a symlink all
// report Exists == false: a folder the transfer has not created yet is a normal
// answer, not an error. Nothing is created, nothing is read recursively and no
// sizes are collected.
func ProbeDir(roots []string, dest, name string) (DirState, error) {
	name, err := cleanProbeName(name)
	if err != nil {
		return DirState{}, err
	}
	resolved, err := resolve(roots, dest, true)
	if err != nil {
		return DirState{}, err
	}
	defer resolved.close()
	if resolved.root == nil {
		// An unmounted root has no directories inside it yet.
		return DirState{}, nil
	}

	rel := path.Join(resolved.rel, name)
	// Lstat every component: a symlink is not followed, so it never counts as
	// an existing destination folder even when it points inside the root.
	parts := strings.Split(rel, "/")
	for i := range parts {
		partial := strings.Join(parts[:i+1], "/")
		info, err := resolved.root.Lstat(partial)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return DirState{}, nil
			}
			return DirState{}, fmt.Errorf("inspect %s: %w", absolutePath(resolved.rootPath, rel), err)
		}
		if info.Mode()&fs.ModeSymlink != 0 || !info.IsDir() {
			return DirState{}, nil
		}
	}

	dir, err := resolved.root.Open(rel)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return DirState{}, nil
		}
		return DirState{}, fmt.Errorf("open %s: %w", absolutePath(resolved.rootPath, rel), err)
	}
	defer dir.Close()
	entries, err := countEntries(dir)
	if err != nil {
		return DirState{}, fmt.Errorf("count %s: %w", absolutePath(resolved.rootPath, rel), err)
	}
	return DirState{Exists: true, Entries: entries}, nil
}

// cleanProbeName keeps one path relative to the destination. The HTTP layer
// normalises cloud paths already; this is the last guard before os.Root.
func cleanProbeName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > maxPathLen || strings.ContainsRune(name, 0) {
		return "", ErrInvalidPath
	}
	cleaned := path.Clean(name)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") || strings.HasPrefix(cleaned, "/") {
		return "", ErrInvalidPath
	}
	return cleaned, nil
}

// countEntries counts the top-level names of an opened directory without
// keeping them and without descending into subdirectories.
func countEntries(dir *os.File) (int, error) {
	entries := 0
	for {
		batch, err := dir.ReadDir(256)
		entries += len(batch)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return entries, nil
			}
			return 0, err
		}
	}
}

// Mkdir creates exactly one directory inside one of roots and returns its
// cleaned absolute path. The parent must already exist: this is a single
// mkdir, never a recursive one, so a typo in the middle of the path fails with
// ErrNotFound instead of silently creating a tree. An existing directory
// returns ErrExists; an existing file returns ErrNotDir.
func Mkdir(roots []string, requested string) (string, error) {
	resolved, err := resolve(roots, requested, true)
	if err != nil {
		return "", err
	}
	defer resolved.close()
	if resolved.root == nil {
		return "", fmt.Errorf("%w: %s", ErrRootUnavailable, resolved.rootPath)
	}

	rel := resolved.rel
	if rel == "" {
		return "", ErrExists
	}
	root := resolved.root

	// resolve accepted the missing tail; make sure everything before it is an
	// existing directory so only the last component is created.
	if parent := path.Dir(rel); parent != "." {
		info, statErr := root.Stat(parent)
		if statErr != nil {
			return "", pathError(statErr)
		}
		if !info.IsDir() {
			return "", ErrNotDir
		}
	}

	info, err := root.Lstat(rel)
	switch {
	case err == nil:
		switch {
		case info.Mode()&fs.ModeSymlink != 0:
			return "", ErrSymlink
		case info.IsDir():
			return "", ErrExists
		default:
			return "", ErrNotDir
		}
	case !errors.Is(err, fs.ErrNotExist):
		return "", fmt.Errorf("inspect %s: %w", resolved.absolute, err)
	}

	if err := root.Mkdir(rel, 0o755); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return "", ErrExists
		}
		return "", fmt.Errorf("create %s: %w", resolved.absolute, err)
	}
	return resolved.absolute, nil
}

// resolvedPath is a requested path mapped to a root and validated inside it.
type resolvedPath struct {
	rootPath string
	rel      string
	absolute string
	root     *os.Root
}

func (p resolvedPath) close() {
	if p.root != nil {
		p.root.Close()
	}
}

// resolve maps requested to a root and walks its existing components through
// os.Root, so browsing and destination validation reject the same paths. With
// allowMissing, a destination that does not exist yet is accepted (rclone
// creates it), and a root that is not mounted is accepted after the lexical
// check because no os.Root can be opened.
func resolve(roots []string, requested string, allowMissing bool) (resolvedPath, error) {
	rootPath, rel, err := matchRoot(roots, requested)
	if err != nil {
		return resolvedPath{}, err
	}
	resolved := resolvedPath{rootPath: rootPath, rel: rel, absolute: absolutePath(rootPath, rel)}
	if !rootExists(rootPath) {
		if allowMissing {
			return resolved, nil
		}
		return resolvedPath{}, fmt.Errorf("%w: %s", ErrRootUnavailable, rootPath)
	}

	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return resolvedPath{}, fmt.Errorf("%w: %s: %v", ErrRootUnavailable, rootPath, err)
	}
	if err := rejectSymlinks(root, rel, allowMissing); err != nil {
		root.Close()
		return resolvedPath{}, err
	}
	resolved.root = root
	return resolved, nil
}

// matchRoot maps requested to a configured root and a path relative to it.
func matchRoot(roots []string, requested string) (rootPath, rel string, err error) {
	requested = strings.TrimSpace(requested)
	if requested == "" || len(requested) > maxPathLen || strings.ContainsRune(requested, 0) {
		return "", "", ErrInvalidPath
	}
	if !path.IsAbs(requested) {
		return "", "", ErrInvalidPath
	}
	cleaned := path.Clean(requested)
	for _, root := range roots {
		root = path.Clean(strings.TrimSpace(root))
		if !path.IsAbs(root) {
			continue
		}
		if cleaned == root {
			return root, "", nil
		}
		if root == "/" {
			return root, strings.TrimPrefix(cleaned, "/"), nil
		}
		if strings.HasPrefix(cleaned, root+"/") {
			return root, strings.TrimPrefix(cleaned, root+"/"), nil
		}
	}
	return "", "", ErrOutsideRoots
}

func rootExists(rootPath string) bool {
	info, err := os.Stat(rootPath)
	return err == nil && info.IsDir()
}

// rejectSymlinks walks every existing component of rel and rejects a symlink.
// With allowMissing, a component that does not exist ends the walk: rclone will
// create the rest of the destination itself.
func rejectSymlinks(root *os.Root, rel string, allowMissing bool) error {
	if rel == "" {
		return nil
	}
	parts := strings.Split(rel, "/")
	for i := range parts {
		partial := strings.Join(parts[:i+1], "/")
		info, err := root.Lstat(partial)
		if err != nil {
			if allowMissing && errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return pathError(err)
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return ErrSymlink
		}
		if !info.IsDir() {
			return ErrNotDir
		}
	}
	return nil
}

// readDirs collects non-recursive subdirectories. Symlinked directories are
// skipped: DirEntry.IsDir reports the entry type without following links.
func readDirs(dir *os.File, absolute string) ([]Entry, bool, error) {
	dirs := make([]Entry, 0, 32)
	scanned := 0
	truncated := false
	for {
		batch, err := dir.ReadDir(256)
		for _, entry := range batch {
			scanned++
			if scanned > maxScanned {
				truncated = true
				break
			}
			if !entry.IsDir() {
				continue
			}
			dirs = append(dirs, Entry{Name: entry.Name(), Path: path.Join(absolute, entry.Name())})
		}
		if truncated {
			break
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, false, fmt.Errorf("read directory: %w", err)
		}
	}

	sort.Slice(dirs, func(i, j int) bool {
		left, right := strings.ToLower(dirs[i].Name), strings.ToLower(dirs[j].Name)
		if left == right {
			return dirs[i].Name < dirs[j].Name
		}
		return left < right
	})
	if len(dirs) > MaxDirs {
		dirs = dirs[:MaxDirs]
		truncated = true
	}
	return dirs, truncated, nil
}

func absolutePath(rootPath, rel string) string {
	if rel == "" {
		return rootPath
	}
	return path.Join(rootPath, rel)
}

func parentPath(rootPath, rel string) *string {
	if rel == "" {
		return nil
	}
	parentRel := path.Dir(rel)
	if parentRel == "." {
		parent := rootPath
		return &parent
	}
	parent := path.Join(rootPath, parentRel)
	return &parent
}

func relOrDot(rel string) string {
	if rel == "" {
		return "."
	}
	return rel
}

func pathError(err error) error {
	if errors.Is(err, fs.ErrNotExist) {
		return ErrNotFound
	}
	return err
}
