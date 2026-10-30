package worker

import (
	"bufio"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/coder/websocket"
)

// The host's graphical apps, from their .desktop files (the freedesktop
// Desktop Entry spec), for the menu (D78): launched in the user's Xpra
// session.

// HostApp is a host app the menu lists.
type HostApp struct {
	// ID is the desktop file ID: "org.gnome.baobab", "kde-kate" for
	// kde/kate.desktop.
	ID       string `json:"id"`
	Name     string `json:"name"`
	Comment  string `json:"comment,omitempty"`
	Icon     string `json:"icon,omitempty"`
	IconPath string `json:"iconPath,omitempty"`
	// Category is the menu's: Accessories, Graphics, Internet, Office,
	// Sound & Video, Administration, Preferences, System Tools, or Other.
	Category string `json:"category"`

	exec []string
}

// desktopEntry is the [Desktop Entry] group's keys, untranslated.
func readDesktopEntry(path string) map[string]string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	keys := map[string]string{}
	in := false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case line == "" || strings.HasPrefix(line, "#"):
		case strings.HasPrefix(line, "["):
			in = line == "[Desktop Entry]"
		case in:
			if k, v, ok := strings.Cut(line, "="); ok {
				keys[strings.TrimSpace(k)] = strings.TrimSpace(v)
			}
		}
	}
	return keys
}

// menuCategory maps freedesktop categories to the Cinnamon menu's, as
// Tilde's own apps are filed (apps/types.ts).
func menuCategory(categories string) string {
	cats := strings.Split(categories, ";")
	has := func(c string) bool { return slices.Contains(cats, c) }
	switch {
	case has("Settings"):
		return "Preferences"
	case has("AudioVideo") || has("Audio") || has("Video"):
		return "Sound & Video"
	case has("Graphics"):
		return "Graphics"
	case has("Network"):
		return "Internet"
	case has("Office"):
		return "Office"
	case has("Development"):
		return "Programming"
	case has("System"):
		return "System Tools"
	case has("Utility"):
		return "Accessories"
	}
	return "Other"
}

// execArgs splits an Exec value into argv: quoted arguments and escapes,
// as the Desktop Entry spec has them. Nothing is opened with the app, so
// an argument with a field code (%f, %u, --file=%f, %i and the rest) is
// dropped whole, rather than left empty or half there.
func execArgs(s string) ([]string, error) {
	var args []string
	var cur strings.Builder
	inQuote, have, field := false, false, false
	end := func() {
		if have && !field {
			args = append(args, cur.String())
		}
		cur.Reset()
		have, field = false, false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case inQuote && c == '\\' && i+1 < len(s):
			i++
			cur.WriteByte(s[i])
		case c == '"':
			inQuote, have = !inQuote, true
		case !inQuote && (c == ' ' || c == '\t'):
			end()
		case c == '%' && i+1 < len(s):
			i++
			if s[i] == '%' {
				cur.WriteByte('%')
			} else {
				field = true
			}
			have = true
		default:
			cur.WriteByte(c)
			have = true
		}
	}
	if inQuote {
		return nil, errors.New("unterminated quote")
	}
	end()
	if len(args) == 0 {
		return nil, errors.New("empty Exec")
	}
	return args, nil
}

// appDataDirs: the user's data folder, then the system's, where
// applications/ and icons/ are.
func appDataDirs() []string {
	home := xdgDir("XDG_DATA_HOME", filepath.Join(os.Getenv("HOME"), ".local", "share"))
	return append([]string{home}, dataDirs()...)
}

// listHostApps reads applications/ in the data dirs: the first file with
// an ID wins (the user's own over the system's), and entries hidden, not
// apps, for terminals, or for some desktop only are left out.
func listHostApps(dirs []string) []HostApp {
	seen := map[string]bool{}
	var apps []HostApp
	for _, d := range dirs {
		root := filepath.Join(d, "applications")
		filepath.WalkDir(root, func(path string, e os.DirEntry, err error) error {
			if err != nil || e.IsDir() || !strings.HasSuffix(path, ".desktop") {
				return nil
			}
			rel, _ := filepath.Rel(root, path)
			id := strings.TrimSuffix(strings.ReplaceAll(rel, string(filepath.Separator), "-"), ".desktop")
			if seen[id] {
				return nil
			}
			seen[id] = true
			k := readDesktopEntry(path)
			if k["Type"] != "Application" || k["Name"] == "" || k["NoDisplay"] == "true" || k["Hidden"] == "true" ||
				k["Terminal"] == "true" || k["OnlyShowIn"] != "" {
				return nil
			}
			if try := k["TryExec"]; try != "" {
				if _, err := lookPath(try); err != nil {
					return nil
				}
			}
			argv, err := execArgs(k["Exec"])
			if err != nil {
				return nil
			}
			a := HostApp{ID: id, Name: clip(k["Name"], 128), Comment: clip(k["Comment"], 256),
				Category: menuCategory(k["Categories"]), exec: argv}
			if icon := k["Icon"]; filepath.IsAbs(icon) {
				a.IconPath = icon
			} else if validIconName(icon) {
				a.Icon = icon
				a.IconPath = findIcon(dirs, icon)
			}
			apps = append(apps, a)
			return nil
		})
	}
	slices.SortFunc(apps, func(a, b HostApp) int { return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)) })
	return apps
}

// lookPath finds TryExec: absolute, or on $PATH.
func lookPath(name string) (string, error) {
	if filepath.IsAbs(name) {
		if _, err := os.Stat(name); err != nil {
			return "", err
		}
		return name, nil
	}
	return exec.LookPath(name)
}

// iconSizes are looked in, best first, in hicolor (where apps install
// their icons) and the pixmaps folder.
var iconSizes = []string{"scalable", "64x64", "48x48", "128x128", "256x256", "32x32", "24x24", "512x512"}

// findIcon finds an app's icon file, for the menu to show (Tilde bundles
// Mint-Y's icons only, which few apps' names are in).
func findIcon(dirs []string, name string) string {
	for _, d := range dirs {
		for _, size := range iconSizes {
			for _, ext := range []string{".svg", ".png"} {
				p := filepath.Join(d, "icons", "hicolor", size, "apps", name+ext)
				if _, err := os.Stat(p); err == nil {
					return p
				}
			}
		}
	}
	for _, d := range dirs {
		for _, ext := range []string{".svg", ".png"} {
			p := filepath.Join(d, "pixmaps", name+ext)
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	return ""
}

func (w *Worker) handleHostApps(rw http.ResponseWriter, r *http.Request) {
	if !w.xpra.available() {
		writeJSON(rw, http.StatusOK, map[string]any{"available": false, "apps": []HostApp{}})
		return
	}
	writeJSON(rw, http.StatusOK, map[string]any{"available": true, "apps": listHostApps(appDataDirs())})
}

// handleHostAppsSession starts the user's Xpra session, for the Host
// Apps window: only the desktop, on Tilde's origin, starts it.
func (w *Worker) handleHostAppsSession(rw http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	switch _, err := w.xpra.ensure(ctx); {
	case errors.Is(err, errNoXpra):
		writeJSON(rw, http.StatusNotFound, apiError{Error: err.Error(), Code: "no_xpra"})
	case err != nil:
		writeJSON(rw, http.StatusBadGateway, apiError{Error: err.Error(), Code: "start_failed"})
	default:
		rw.WriteHeader(http.StatusNoContent)
	}
}

// handleHostAppLaunch starts a host app in the user's Xpra session.
func (w *Worker) handleHostAppLaunch(rw http.ResponseWriter, r *http.Request) {
	var req struct {
		ID string `json:"id"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(rw, err)
		return
	}
	var app *HostApp
	for _, a := range listHostApps(appDataDirs()) {
		if a.ID == req.ID {
			app = &a
			break
		}
	}
	if app == nil {
		writeJSON(rw, http.StatusNotFound, apiError{Error: "no such app", Code: "not_found"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	switch err := w.xpra.launch(ctx, app.exec); {
	case errors.Is(err, errNoXpra):
		writeJSON(rw, http.StatusNotFound, apiError{Error: err.Error(), Code: "no_xpra"})
	case err != nil:
		writeJSON(rw, http.StatusBadGateway, apiError{Error: err.Error(), Code: "launch_failed"})
	default:
		rw.WriteHeader(http.StatusNoContent)
	}
}

// handleHostAppsConnect connects Tilde's own Xpra client (D80) to the
// user's Xpra session: the browser frames Xpra's packets itself, and the
// worker relays the bytes, in WebSocket messages of any size, to and from
// the session's socket. Like a dial for the Xpra port, it never starts the
// session; the desktop does (session, launch).
//
// The Origin check happens in the main server before the upgrade is
// proxied here.
func (w *Worker) handleHostAppsConnect(rw http.ResponseWriter, r *http.Request) {
	// A plain GET passes the server without an Origin check: it must not
	// make the worker connect to Xpra.
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		writeJSON(rw, http.StatusUpgradeRequired, apiError{Error: "a WebSocket only", Code: "upgrade_required"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	xc, err := w.xpra.dial(ctx)
	cancel()
	switch {
	case errors.Is(err, errXpraNotRunning):
		writeJSON(rw, http.StatusNotFound, apiError{Error: err.Error(), Code: "not_running"})
		return
	case err != nil:
		slog.Info("worker: connect to Xpra", "err", err)
		writeJSON(rw, http.StatusBadGateway, apiError{Error: err.Error(), Code: "unreachable"})
		return
	}
	c, err := websocket.Accept(rw, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		xc.Close()
		return
	}
	relayXpra(c, xc)
}
