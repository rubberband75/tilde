package worker

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// Drafts are the text editor's unsaved changes, kept by the worker in
// $XDG_STATE_HOME/tilde/drafts (D48). Locking clears the page (D40) and a
// browser can reload or crash: the editor saves its text here as the user
// types, and finds it again when its window comes back, in this browser
// or another. A draft is named by a key: "file:" and the file's path, or
// "new:" and an id for a document not saved yet. Saving or discarding the
// changes deletes it.

// Draft is unsaved text.
type Draft struct {
	Key  string `json:"key"`
	Text string `json:"text"`
	// Base is the file's version the changes started from; if the file
	// has another one now, it changed on disk meanwhile.
	Base    string    `json:"base,omitempty"`
	Updated time.Time `json:"updated"`
}

const maxDraftKey = 4200

func (w *Worker) draftFile(r *http.Request) (string, string, error) {
	key := r.URL.Query().Get("key")
	if key == "" || len(key) > maxDraftKey {
		return "", "", badRequest{"missing or invalid draft key"}
	}
	sum := sha256.Sum256([]byte(key))
	return key, filepath.Join(w.drafts, hex.EncodeToString(sum[:])+".json"), nil
}

func (w *Worker) handleDraftGet(rw http.ResponseWriter, r *http.Request) {
	key, file, err := w.draftFile(r)
	if err != nil {
		writeErr(rw, err)
		return
	}
	data, err := os.ReadFile(file)
	if errors.Is(err, fs.ErrNotExist) {
		writeJSON(rw, http.StatusNotFound, apiError{Error: "no draft", Code: "not_found"})
		return
	}
	if err != nil {
		writeErr(rw, err)
		return
	}
	var d Draft
	if err := json.Unmarshal(data, &d); err != nil || d.Key != key {
		writeJSON(rw, http.StatusNotFound, apiError{Error: "no draft", Code: "not_found"})
		return
	}
	writeJSON(rw, http.StatusOK, d)
}

func (w *Worker) handleDraftPut(rw http.ResponseWriter, r *http.Request) {
	key, file, err := w.draftFile(r)
	if err != nil {
		writeErr(rw, err)
		return
	}
	var req struct {
		Text string `json:"text"`
		Base string `json:"base"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(rw, r.Body, 2*maxTextSize))
	if err := dec.Decode(&req); err != nil {
		writeErr(rw, badRequest{"invalid JSON body: " + err.Error()})
		return
	}
	if len(req.Text) > maxTextSize {
		writeJSON(rw, http.StatusRequestEntityTooLarge, apiError{Error: "the text is too large (over 10 MB)", Code: "too_large"})
		return
	}
	data, err := json.Marshal(Draft{Key: key, Text: req.Text, Base: req.Base, Updated: time.Now().UTC()})
	if err != nil {
		writeErr(rw, err)
		return
	}
	// Only the user may read them: drafts are file contents.
	if err := os.MkdirAll(w.drafts, 0o700); err != nil {
		writeErr(rw, err)
		return
	}
	if err := writeFileAtomic(file, data, 0o600); err != nil {
		writeErr(rw, err)
		return
	}
	rw.WriteHeader(http.StatusNoContent)
}

func (w *Worker) handleDraftDelete(rw http.ResponseWriter, r *http.Request) {
	_, file, err := w.draftFile(r)
	if err != nil {
		writeErr(rw, err)
		return
	}
	if err := os.Remove(file); err != nil && !errors.Is(err, fs.ErrNotExist) {
		writeErr(rw, err)
		return
	}
	rw.WriteHeader(http.StatusNoContent)
}
