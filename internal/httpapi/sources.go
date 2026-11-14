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

// parseSources turns the request fields into the cloud source of a job.
// A missing sourcePaths keeps the legacy single-folder behaviour; an explicit
// empty array means the whole disk.
func parseSources(sourcePath string, sourcePaths *[]string) (jobs.Source, error) {
	legacy := strings.TrimSpace(sourcePath)
	if sourcePaths != nil && legacy != "" {
		return jobs.Source{}, &sourceFieldError{
			field:   "sourcePaths",
			message: "Укажите либо одну папку, либо список папок.",
		}
	}
	if sourcePaths == nil {
		if legacy == "" {
			return jobs.Source{}, nil
		}
		cleaned, err := normalizeSourcePath(legacy)
		if err != nil {
			return jobs.Source{}, &sourceFieldError{field: "sourcePath", message: err.Error()}
		}
		return jobs.Source{Path: cleaned}, nil
	}

	paths, err := normalizeSourcePaths(*sourcePaths)
	if err != nil {
		return jobs.Source{}, err
	}
	if len(paths) == 0 {
		return jobs.Source{}, nil
	}
	return jobs.Source{Paths: paths}, nil
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
