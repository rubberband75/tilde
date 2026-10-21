package worker

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Desktop icon layout (D36). Tilde keeps its own layout in grid cells,
// because a browser window is rarely the size of the screen Nemo arranged
// the icons for. Until the user arranges icons in Tilde, the layout is
// seeded from Nemo's: per-file GVfs metadata (metadata::nemo-icon-position)
// and ~/.config/nemo/desktop-metadata for Computer, Home and Trash.
//
// Icons are keyed by file name in the Desktop folder; the built-in icons use
// keys that cannot be file names (they contain "/").

// Cell is a desktop grid position, counted from the top left.
type Cell struct {
	Col int `json:"col"`
	Row int `json:"row"`
}

// NemoIcon is where Nemo put an icon, in screen pixels.
type NemoIcon struct {
	X int `json:"x"`
	Y int `json:"y"`
}

// NemoLayout is Nemo's desktop arrangement, for seeding Tilde's.
type NemoLayout struct {
	// Grid is Nemo's cell size in pixels (desktop-grid-adjust), if known.
	Grid  *[2]int             `json:"grid,omitempty"`
	Icons map[string]NemoIcon `json:"icons"`
}

// DesktopLayout is GET /api/v1/desktop/layout.
type DesktopLayout struct {
	// Saved is false until the user arranges icons in Tilde.
	Saved     bool            `json:"saved"`
	Positions map[string]Cell `json:"positions"`
	// Nemo is sent only while nothing is saved.
	Nemo *NemoLayout `json:"nemo,omitempty"`
}

const (
	maxDesktopIcons = 5000
	maxDesktopCell  = 1000
	// Nemo is asked about at most this many files; a desktop with more is
	// laid out automatically.
	maxNemoFiles = 500
)

// Built-in desktop icons, with their Nemo desktop-metadata sections.
var desktopSpecials = map[string]string{
	"computer:///": "computer",
	"home:///":     "home",
	"trash:///":    "trash",
}

// gioCommand runs GIO's command-line tool; tests replace it.
var gioCommand = "gio"

var desktopLayoutMu sync.Mutex

func (w *Worker) desktopLayoutFile() string {
	return filepath.Join(w.configHome, "tilde", "desktop-layout.json")
}

type desktopLayoutFile struct {
	Version   int             `json:"version"`
	Positions map[string]Cell `json:"positions"`
}

// readDesktopLayout returns the saved positions, or nil if none are saved.
func (w *Worker) readDesktopLayout() (map[string]Cell, error) {
	b, err := os.ReadFile(w.desktopLayoutFile())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var f desktopLayoutFile
	if err := json.Unmarshal(b, &f); err != nil || f.Positions == nil {
		// A damaged file is treated as an empty layout, not an error that
		// would leave the desktop blank.
		return map[string]Cell{}, nil
	}
	return f.Positions, nil
}

func (w *Worker) handleDesktopLayout(rw http.ResponseWriter, r *http.Request) {
	desktopLayoutMu.Lock()
	pos, err := w.readDesktopLayout()
	desktopLayoutMu.Unlock()
	if err != nil {
		writeErr(rw, err)
		return
	}
	out := DesktopLayout{Saved: pos != nil, Positions: pos}
	if pos == nil {
		out.Positions = map[string]Cell{}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		out.Nemo = w.nemoLayout(ctx)
	}
	writeJSON(rw, http.StatusOK, out)
}

// handleDesktopLayoutSave merges positions into the layout (null removes
// one), or replaces it with replace. Merging lets two clients that move
// different icons keep each other's changes.
func (w *Worker) handleDesktopLayoutSave(rw http.ResponseWriter, r *http.Request) {
	var req struct {
		Positions map[string]*Cell `json:"positions"`
		Replace   bool             `json:"replace"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(rw, err)
		return
	}
	for k, c := range req.Positions {
		if !validDesktopKey(k) {
			writeErr(rw, badRequest{"invalid icon key"})
			return
		}
		if c != nil && (c.Col < 0 || c.Row < 0 || c.Col > maxDesktopCell || c.Row > maxDesktopCell) {
			writeErr(rw, badRequest{"position out of range"})
			return
		}
	}

	desktopLayoutMu.Lock()
	defer desktopLayoutMu.Unlock()
	pos, err := w.readDesktopLayout()
	if err != nil {
		writeErr(rw, err)
		return
	}
	if pos == nil || req.Replace {
		pos = map[string]Cell{}
	}
	for k, c := range req.Positions {
		if c == nil {
			delete(pos, k)
		} else {
			pos[k] = *c
		}
	}
	if len(pos) > maxDesktopIcons {
		writeErr(rw, badRequest{"too many icons"})
		return
	}
	b, err := json.MarshalIndent(desktopLayoutFile{Version: 1, Positions: pos}, "", "  ")
	if err != nil {
		writeErr(rw, err)
		return
	}
	if err := writeFileAtomic(w.desktopLayoutFile(), append(b, '\n'), 0o644); err != nil {
		writeErr(rw, err)
		return
	}
	writeJSON(rw, http.StatusOK, DesktopLayout{Saved: true, Positions: pos})
}

// validDesktopKey accepts a file name or a built-in icon.
func validDesktopKey(k string) bool {
	if _, ok := desktopSpecials[k]; ok {
		return true
	}
	return k != "" && k != "." && k != ".." && len(k) <= 255 && !strings.ContainsAny(k, "/\x00")
}

// nemoLayout reads Nemo's arrangement of the desktop. Anything missing
// (no Nemo, no GVfs, no gio) just leaves icons out; the result is a hint.
func (w *Worker) nemoLayout(ctx context.Context) *NemoLayout {
	out := &NemoLayout{Icons: map[string]NemoIcon{}}
	monitors := map[string]int{}

	sections := readKeyFile(filepath.Join(w.configHome, "nemo", "desktop-metadata"))
	for key, section := range desktopSpecials {
		if icon, ok := parseNemoPosition(sections[section]["nemo-icon-position"]); ok {
			out.Icons[key] = icon
			monitors[key], _ = strconv.Atoi(sections[section]["monitor"])
		}
	}

	desktop := userDirs(w.info.Home, w.configHome)["DESKTOP"]
	if entries, err := os.ReadDir(desktop); err == nil && len(entries) > 0 && len(entries) <= maxNemoFiles {
		paths := make([]string, len(entries))
		for i, e := range entries {
			paths[i] = filepath.Join(desktop, e.Name())
		}
		for name, m := range gioNemoPositions(ctx, paths) {
			out.Icons[name] = m.icon
			monitors[name] = m.monitor
		}
	}

	// Icons on other monitors would land on top of these; keep the monitor
	// most icons are on.
	count := map[int]int{}
	best := 0
	for _, m := range monitors {
		count[m]++
		if count[m] > count[best] || (count[m] == count[best] && m < best) {
			best = m
		}
	}
	for k, m := range monitors {
		if m != best {
			delete(out.Icons, k)
		}
	}

	if g := strings.Split(sections["desktop-monitor-"+strconv.Itoa(best)]["desktop-grid-adjust"], ";"); len(g) >= 2 {
		gw, err1 := strconv.Atoi(g[0])
		gh, err2 := strconv.Atoi(g[1])
		if err1 == nil && err2 == nil && gw > 0 && gh > 0 {
			out.Grid = &[2]int{gw, gh}
		}
	}
	return out
}

type nemoFile struct {
	icon    NemoIcon
	monitor int
}

// gioNemoPositions asks GIO for the Nemo position of each path. GIO reads
// the GVfs metadata database directly, as Nemo does; writes would go through
// gvfsd-metadata, but Tilde never writes Nemo's positions.
func gioNemoPositions(ctx context.Context, paths []string) map[string]nemoFile {
	args := append([]string{"info", "-a", "metadata::nemo-icon-position,metadata::monitor"}, paths...)
	cmd := exec.CommandContext(ctx, gioCommand, args...)
	// The labels ("uri:", "local path:") are translated; file names must
	// stay in the user's charset, so only messages are switched to C.
	for _, kv := range os.Environ() {
		switch {
		case strings.HasPrefix(kv, "LC_ALL="):
			// LC_ALL would override LC_MESSAGES; keep its charset.
			cmd.Env = append(cmd.Env, "LC_CTYPE="+strings.TrimPrefix(kv, "LC_ALL="))
		case strings.HasPrefix(kv, "LANGUAGE="), strings.HasPrefix(kv, "LC_MESSAGES="):
		default:
			cmd.Env = append(cmd.Env, kv)
		}
	}
	cmd.Env = append(cmd.Env, "LC_MESSAGES=C")
	// gio exits non-zero if one file vanished meanwhile; the others are
	// still printed.
	out, _ := cmd.Output()
	return parseGioInfo(string(out))
}

// parseGioInfo reads "gio info" output: a block per file starting with
// "uri:", with "local path:" and indented "metadata::" attributes.
func parseGioInfo(out string) map[string]nemoFile {
	res := map[string]nemoFile{}
	var name string
	var cur nemoFile
	var hasPos bool
	flush := func() {
		if name != "" && hasPos {
			res[name] = cur
		}
		name, cur, hasPos = "", nemoFile{}, false
	}
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "uri: ") {
			flush()
			continue
		}
		if p, ok := strings.CutPrefix(line, "local path: "); ok {
			name = filepath.Base(p)
			continue
		}
		k, v, ok := strings.Cut(strings.TrimSpace(line), ": ")
		if !ok {
			continue
		}
		switch k {
		case "metadata::nemo-icon-position":
			cur.icon, hasPos = parseNemoPosition(v)
		case "metadata::monitor":
			cur.monitor, _ = strconv.Atoi(v)
		}
	}
	flush()
	return res
}

// parseNemoPosition parses "x,y".
func parseNemoPosition(s string) (NemoIcon, bool) {
	xs, ys, ok := strings.Cut(strings.TrimSpace(s), ",")
	if !ok {
		return NemoIcon{}, false
	}
	x, err1 := strconv.Atoi(xs)
	y, err2 := strconv.Atoi(ys)
	if err1 != nil || err2 != nil || x < 0 || y < 0 {
		return NemoIcon{}, false
	}
	return NemoIcon{X: x, Y: y}, true
}

// readKeyFile reads a GLib key file into sections of keys. Comments,
// locales and escapes are ignored; Nemo's desktop-metadata needs none.
func readKeyFile(path string) map[string]map[string]string {
	out := map[string]map[string]string{}
	b, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	var section map[string]string
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "" || strings.HasPrefix(line, "#"):
		case strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]"):
			section = map[string]string{}
			out[line[1:len(line)-1]] = section
		case section != nil:
			if k, v, ok := strings.Cut(line, "="); ok {
				section[strings.TrimSpace(k)] = strings.TrimSpace(v)
			}
		}
	}
	return out
}
