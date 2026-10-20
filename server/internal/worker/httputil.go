package worker

import (
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"path/filepath"
	"syscall"
)

// apiError is the JSON body of every error response.
type apiError struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

// badRequest marks an error as the client's fault (HTTP 400).
type badRequest struct{ msg string }

func (e badRequest) Error() string { return e.msg }

// ErrOtherDrive is returned when an operation would cross filesystems.
var ErrOtherDrive = errors.New("the file is on a different drive")

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeErr maps filesystem errors to HTTP statuses. The kernel already made
// the permission decision; this only reports it.
func writeErr(w http.ResponseWriter, err error) {
	status, code := http.StatusInternalServerError, "internal_error"
	var br badRequest
	switch {
	case errors.As(err, &br):
		status, code = http.StatusBadRequest, "bad_request"
	case errors.Is(err, fs.ErrNotExist):
		status, code = http.StatusNotFound, "not_found"
	case errors.Is(err, fs.ErrPermission):
		status, code = http.StatusForbidden, "permission_denied"
	case errors.Is(err, fs.ErrExist):
		status, code = http.StatusConflict, "exists"
	case errors.Is(err, ErrOtherDrive), errors.Is(err, syscall.EXDEV):
		status, code = http.StatusConflict, "other_drive"
	case errors.Is(err, syscall.ENOTEMPTY):
		status, code = http.StatusConflict, "not_empty"
	case errors.Is(err, syscall.ENOTDIR):
		status, code = http.StatusBadRequest, "not_a_directory"
	case errors.Is(err, syscall.EISDIR):
		status, code = http.StatusBadRequest, "is_a_directory"
	case errors.Is(err, syscall.ENOSPC):
		status, code = http.StatusInsufficientStorage, "no_space"
	case errors.Is(err, syscall.EROFS):
		status, code = http.StatusForbidden, "read_only"
	}
	if status == http.StatusInternalServerError {
		slog.Error("worker: request failed", "err", err)
	}
	writeJSON(w, status, apiError{Error: err.Error(), Code: code})
}

// queryPath reads an absolute path from the query string.
func queryPath(r *http.Request, key string) (string, error) {
	return cleanAbs(r.URL.Query().Get(key))
}

func cleanAbs(p string) (string, error) {
	if p == "" {
		return "", badRequest{"missing path"}
	}
	if !filepath.IsAbs(p) {
		return "", badRequest{"path must be absolute: " + p}
	}
	return filepath.Clean(p), nil
}

// validName rejects names that are not a single path element.
func validName(name string) bool {
	return name != "" && name != "." && name != ".." && filepath.Base(name) == name
}

func decodeJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	if err := dec.Decode(v); err != nil {
		return badRequest{"invalid JSON body: " + err.Error()}
	}
	return nil
}
