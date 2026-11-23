package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"path"
	"sort"
	"strings"
	"unicode/utf8"

	"cloudferry/internal/jobs"
)

// maxSourcePaths bounds one multi-folder download. It is checked after
// normalisation (duplicates and descendants are already dropped), so the user
// can always fit the folders the picker actually allows.
const maxSourcePaths = 20

// maxSourceFiles bounds the individual files in one download. A folder and a
// file selection together stay at most maxSourcePaths+maxSourceFiles items.
const maxSourceFiles = 100

// maxSourcePathLen bounds one cloud path before it reaches rclone.
const maxSourcePathLen = 1024

// sourceFieldError is a validation error that names the request field the UI
// must mark: "sourcePath" for the legacy single folder, "sourcePaths" for the
// list. The HTTP answer carries it as {"error": "...", "field": "..."}.
type sourceFieldError struct {
	field   string
	message string
}

func (e *sourceFieldError) Error() string { return e.message }

// writeFieldError answers a bad request and names the form field to highlight.
func writeFieldError(w http.ResponseWriter, status int, field, message string) {
	writeJSON(w, status, map[string]string{"error": message, "field": field})
}

// parseSources turns the request fields into the cloud source of a job. A
// missing sourcePaths/sourceFiles keeps the legacy single-folder behaviour; an
// explicit empty array means the whole disk. Files may be combined with the
// folder list, but not with the legacy single folder.
func parseSources(sourcePath string, sourcePaths, sourceFiles *[]string) (jobs.Source, error) {
	legacy := strings.TrimSpace(sourcePath)
	if legacy != "" && (sourcePaths != nil || sourceFiles != nil) {
		field := "sourcePaths"
		if sourcePaths == nil {
			field = "sourceFiles"
		}
		return jobs.Source{}, &sourceFieldError{
			field:   field,
			message: "Укажите либо одну папку, либо список папок и файлов.",
		}
	}
	if sourcePaths == nil && sourceFiles == nil {
		if legacy == "" {
			return jobs.Source{}, nil
		}
		cleaned, err := normalizeSourcePath(legacy)
		if err != nil {
			return jobs.Source{}, &sourceFieldError{field: "sourcePath", message: err.Error()}
		}
		return jobs.Source{Path: cleaned}, nil
	}

	var paths, files []string
	if sourcePaths != nil {
		normalized, err := normalizeSourcePaths(*sourcePaths)
		if err != nil {
			return jobs.Source{}, err
		}
		paths = normalized
	}
	if sourceFiles != nil {
		normalized, err := normalizeSourceFiles(*sourceFiles)
		if err != nil {
			return jobs.Source{}, err
		}
		files = normalized
	}
	if len(paths) == 0 && len(files) == 0 {
		return jobs.Source{}, nil
	}
	return jobs.Source{Paths: paths, Files: files}, nil
}

func parseSelection(path string, paths, files *[]string, excluded []string) (jobs.Source, error) {
	src, err := parseSources(path, paths, files)
	if err != nil || len(excluded) == 0 {
		return src, err
	}
	if src.Path != "" || len(src.Paths) > 0 || len(src.Files) > 0 {
		return jobs.Source{}, &sourceFieldError{"excludedFolders", "Исключения применяются к целому диску."}
	}
	roots, err := normalizeSourcePaths(excluded)
	if err != nil {
		return jobs.Source{}, &sourceFieldError{"excludedFolders", err.Error()}
	}
	src.Excluded = roots
	return src, nil
}

// normalizeSourcePaths validates every path, drops duplicates, then sorts the
// list and drops descendants of selected ancestors: selecting "Фото" already
// covers "Фото/2024", so the shorter path wins.
func normalizeSourcePaths(raw []string) ([]string, error) {
	cleaned := make([]string, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for _, item := range raw {
		value, err := normalizeSourcePath(item)
		if err != nil {
			return nil, &sourceFieldError{field: "sourcePaths", message: err.Error()}
		}
		if _, dup := seen[value]; dup {
			continue
		}
		seen[value] = struct{}{}
		cleaned = append(cleaned, value)
	}
	sort.Strings(cleaned)

	kept := cleaned[:0]
	for _, value := range cleaned {
		covered := false
		for _, ancestor := range kept {
			if strings.HasPrefix(value, ancestor+"/") {
				covered = true
				break
			}
		}
		if !covered {
			kept = append(kept, value)
		}
	}
	if len(kept) > maxSourcePaths {
		return nil, &sourceFieldError{
			field:   "sourcePaths",
			message: fmt.Sprintf("Можно выбрать не больше %d папок за один раз.", maxSourcePaths),
		}
	}
	if kept == nil {
		return []string{}, nil
	}
	return kept, nil
}

// normalizeSourceFiles validates every file path the same way as a folder
// path, drops duplicates and sorts the list. A file path keeps its exact
// spelling (no trailing slash, no ".."): it selects one file, not a subtree.
func normalizeSourceFiles(raw []string) ([]string, error) {
	cleaned := make([]string, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for _, item := range raw {
		value, err := normalizeSourcePath(item)
		if err != nil {
			return nil, &sourceFieldError{field: "sourceFiles", message: err.Error()}
		}
		if _, dup := seen[value]; dup {
			continue
		}
		seen[value] = struct{}{}
		cleaned = append(cleaned, value)
	}
	sort.Strings(cleaned)
	if len(cleaned) > maxSourceFiles {
		return nil, &sourceFieldError{
			field:   "sourceFiles",
			message: fmt.Sprintf("Можно выбрать не больше %d файлов за один раз.", maxSourceFiles),
		}
	}
	if cleaned == nil {
		return []string{}, nil
	}
	return cleaned, nil
}

// normalizeSourcePath makes one cloud folder path canonical: no surrounding
// slashes, no "." or ".." segments, no control characters and a bounded
// length. The cloud root itself is not selectable: an empty choice means the
// whole disk.
func normalizeSourcePath(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", errors.New("Укажите папку в облаке.")
	}
	if len(value) > maxSourcePathLen || !utf8.ValidString(value) {
		return "", errors.New("Путь в облаке слишком длинный.")
	}
	if strings.IndexFunc(value, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
		return "", errors.New("В пути в облаке есть недопустимые символы.")
	}
	value = strings.Trim(value, "/")
	if value == "" {
		return "", errors.New("Укажите папку в облаке, а не весь Диск.")
	}
	cleaned := path.Clean(value)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") || strings.Contains(cleaned, "/../") {
		return "", errors.New("Проверьте путь в облаке.")
	}
	if strings.HasPrefix(cleaned, "/") {
		return "", errors.New("Укажите папку в облаке без косой черты в начале.")
	}
	return cleaned, nil
}

// sourceError answers a validation error; anything else is an internal
// surprise and is reported without internals.
func (s *Server) sourceError(w http.ResponseWriter, err error) {
	var fieldErr *sourceFieldError
	if errors.As(err, &fieldErr) {
		writeFieldError(w, http.StatusBadRequest, fieldErr.field, fieldErr.message)
		return
	}
	s.fail(w, http.StatusInternalServerError, "Не удалось проверить папки в облаке.", err)
}

// sourceNotFound answers a verified-missing cloud folder with 400, naming the
// folder and the field the user should fix.
func sourceNotFound(w http.ResponseWriter, field string, err error) {
	var notFound *jobs.SourceNotFoundError
	if errors.As(err, &notFound) && notFound.Path != "" {
		writeFieldError(w, http.StatusBadRequest, field,
			fmt.Sprintf("Папка в облаке не найдена: «%s».", notFound.Path))
		return
	}
	writeFieldError(w, http.StatusBadRequest, field, "Папка в облаке не найдена.")
}

// sourceFileNotFound answers a verified-missing cloud file with 400, naming the
// file and the field the user should fix. A folder where a file was expected is
// reported separately: it is a mix-up, not a vanished file.
func sourceFileNotFound(w http.ResponseWriter, err error) {
	var fileErr *jobs.SourceFileError
	if errors.As(err, &fileErr) && fileErr.Path != "" {
		if fileErr.IsDir {
			writeFieldError(w, http.StatusBadRequest, "sourceFiles",
				fmt.Sprintf("«%s» — это папка, а не файл. Выберите файл.", fileErr.Path))
			return
		}
		writeFieldError(w, http.StatusBadRequest, "sourceFiles",
			fmt.Sprintf("Файл не найден: «%s».", fileErr.Path))
		return
	}
	writeFieldError(w, http.StatusBadRequest, "sourceFiles", "Файл не найден в облаке.")
}

// sourceField names the request field behind a source: the list for a
// multi-folder source, the legacy path otherwise.
func sourceField(src jobs.Source) string {
	return sourceFieldForPaths(src.Paths)
}

// sourceFieldForPaths names the request field for a stored folder list.
func sourceFieldForPaths(paths []string) string {
	if len(paths) > 0 {
		return "sourcePaths"
	}
	return "sourcePath"
}
