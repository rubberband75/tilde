package worker

import (
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
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

// codedErr is a client error with its own status and code, for the
// frontend to act on (a Wi-Fi network that needs a password).
type codedErr struct {
	status    int
	code, msg string
}

func (e codedErr) Error() string { return e.msg }

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
	status, code := classifyErr(err)
	if status == http.StatusInternalServerError {
		slog.Error("worker: request failed", "err", err)
	}
	writeJSON(w, status, apiError{Error: err.Error(), Code: code})
}

// classifyErr returns the HTTP status and API error code for err. Jobs use
// the code for per-item errors too.
func classifyErr(err error) (int, string) {
	var br badRequest
	var ce codedErr
	switch {
	case errors.As(err, &ce):
		return ce.status, ce.code
	case errors.As(err, &br):
		return http.StatusBadRequest, "bad_request"
	case errors.Is(err, fs.ErrNotExist):
		return http.StatusNotFound, "not_found"
	case errors.Is(err, fs.ErrPermission):
		return http.StatusForbidden, "permission_denied"
	case errors.Is(err, fs.ErrExist):
		return http.StatusConflict, "exists"
	case errors.Is(err, ErrOtherDrive), errors.Is(err, syscall.EXDEV):
		return http.StatusConflict, "other_drive"
	case errors.Is(err, syscall.ENOTEMPTY):
		return http.StatusConflict, "not_empty"
	case errors.Is(err, syscall.ENOTDIR):
		return http.StatusBadRequest, "not_a_directory"
	case errors.Is(err, syscall.EISDIR):
		return http.StatusBadRequest, "is_a_directory"
	case errors.Is(err, syscall.ENOSPC):
		return http.StatusInsufficientStorage, "no_space"
	case errors.Is(err, syscall.EROFS):
		return http.StatusForbidden, "read_only"
	}
	return http.StatusInternalServerError, "internal_error"
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

// writeFileAtomic replaces file with data through a temporary file in the
// same folder, so readers (Nemo, other clients) never see half a file.
func writeFileAtomic(file string, data []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(file), "."+filepath.Base(file)+"-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), perm); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), file)
}
