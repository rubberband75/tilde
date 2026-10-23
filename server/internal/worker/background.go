package worker

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
)

// Desktop background (D37). By default the desktop shows the user's
// Cinnamon background (org.cinnamon.desktop.background, read with
// gsettings). A background picked in Tilde is Tilde's own setting, in
// ~/.config/tilde/desktop-background.json, so trying one out in a browser
// never changes the real desktop; resetting it follows Cinnamon again.

// Background describes the desktop background in Cinnamon's terms.
type Background struct {
	// Source is "tilde", "cinnamon", or "default" (neither is set: the
	// theme's background).
	Source string `json:"source"`
	// Picture is an absolute path; empty for colors only.
	Picture string `json:"picture"`
	// Options is Cinnamon's picture-options: none, wallpaper (tiled),
	// centered, scaled, stretched, zoom or spanned.
	Options   string `json:"options"`
	Primary   string `json:"primary"`   // #rrggbb
	Secondary string `json:"secondary"` // #rrggbb, for gradients
	// Shading is Cinnamon's color-shading-type: solid, vertical, horizontal.
	Shading string `json:"shading"`
}

var (
	backgroundOptions = []string{"none", "wallpaper", "centered", "scaled", "stretched", "zoom", "spanned"}
	backgroundShading = []string{"solid", "vertical", "horizontal"}
	hexColor          = regexp.MustCompile(`^#([0-9a-fA-F]{6}|[0-9a-fA-F]{12})$`)
)

// gsettingsCommand runs GLib's gsettings tool; tests replace it.
var gsettingsCommand = "gsettings"

var backgroundMu sync.Mutex

func (w *Worker) backgroundFile() string {
	return filepath.Join(w.configHome, "tilde", "desktop-background.json")
}

func defaultBackground() Background {
	return Background{Source: "default", Options: "zoom", Primary: "#000000", Secondary: "#000000", Shading: "solid"}
}

// normalize fills in defaults and drops values Tilde cannot show. Colors in
// GDK's 16-bit form (#rrrrggggbbbb) keep their high bytes.
func (b *Background) normalize() {
	d := defaultBackground()
	if !slices.Contains(backgroundOptions, b.Options) {
		b.Options = d.Options
	}
	if !slices.Contains(backgroundShading, b.Shading) {
		b.Shading = d.Shading
	}
	for _, c := range []*string{&b.Primary, &b.Secondary} {
		switch {
		case !hexColor.MatchString(*c):
			*c = d.Primary
		case len(*c) == 13:
			*c = "#" + (*c)[1:3] + (*c)[5:7] + (*c)[9:11]
		}
		*c = strings.ToLower(*c)
	}
	if b.Picture != "" && (!filepath.IsAbs(b.Picture) || filepath.Clean(b.Picture) != b.Picture) {
		b.Picture = ""
	}
}

// background returns the background to show.
func (w *Worker) background(ctx context.Context) Background {
	backgroundMu.Lock()
	data, err := os.ReadFile(w.backgroundFile())
	backgroundMu.Unlock()
	if err == nil {
		var b Background
		if json.Unmarshal(data, &b) == nil {
			b.Source = "tilde"
			b.normalize()
			return b
		}
	}
	if b, ok := cinnamonBackground(ctx); ok {
		return b
	}
	return defaultBackground()
}

// cinnamonBackground reads org.cinnamon.desktop.background.
func cinnamonBackground(ctx context.Context) (Background, bool) {
	keys, ok := gsettingsList(ctx, "org.cinnamon.desktop.background")
	if !ok {
		// No gsettings, or no Cinnamon schema (not a Mint desktop).
		return Background{}, false
	}
	b := Background{
		Source:    "cinnamon",
		Options:   gvariantString(keys["picture-options"]),
		Primary:   gvariantString(keys["primary-color"]),
		Secondary: gvariantString(keys["secondary-color"]),
		Shading:   gvariantString(keys["color-shading-type"]),
	}
	// Slideshow XML files and remote pictures are not shown.
	if p, err := pathFromURI(gvariantString(keys["picture-uri"])); err == nil && !strings.HasSuffix(p, ".xml") {
		b.Picture = p
	}
	b.normalize()
	return b, true
}

// gsettingsList reads every key of a schema, as GVariant text by key name.
// gsettings reads dconf's database directly, without the session bus. It
// fails when gsettings or the schema is not installed.
func gsettingsList(ctx context.Context, schema string) (map[string]string, bool) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, gsettingsCommand, "list-recursively", schema).Output()
	if err != nil {
		return nil, false
	}
	keys := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		// "org.cinnamon.desktop.background picture-uri 'file:///…'"
		parts := strings.SplitN(sc.Text(), " ", 3)
		if len(parts) == 3 && parts[0] == schema {
			keys[parts[1]] = parts[2]
		}
	}
	return keys, true
}

// gvariantString unquotes a GVariant text string ('…' or "…" with
// backslash escapes). Anything else is returned as is.
func gvariantString(s string) string {
	s = strings.TrimSpace(s)
	if len(s) < 2 || (s[0] != '\'' && s[0] != '"') || s[len(s)-1] != s[0] {
		return s
	}
	var b strings.Builder
	for i := 1; i < len(s)-1; i++ {
		if s[i] == '\\' && i+1 < len(s)-1 {
			i++
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func (w *Worker) handleBackground(rw http.ResponseWriter, r *http.Request) {
	writeJSON(rw, http.StatusOK, w.background(r.Context()))
}

// handleBackgroundSet saves Tilde's own background, or with reset removes
// it so the desktop follows Cinnamon again.
func (w *Worker) handleBackgroundSet(rw http.ResponseWriter, r *http.Request) {
	var req struct {
		Background
		Reset bool `json:"reset"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(rw, err)
		return
	}
	backgroundMu.Lock()
	var err error
	if req.Reset {
		err = os.Remove(w.backgroundFile())
		if errors.Is(err, fs.ErrNotExist) {
			err = nil
		}
	} else {
		b := req.Background
		if b.Picture != "" && (!filepath.IsAbs(b.Picture) || filepath.Clean(b.Picture) != b.Picture) {
			backgroundMu.Unlock()
			writeErr(rw, badRequest{"picture must be an absolute path"})
			return
		}
		b.Source = ""
		b.normalize()
		var data []byte
		data, err = json.MarshalIndent(b, "", "  ")
		if err == nil {
			err = writeFileAtomic(w.backgroundFile(), append(data, '\n'), 0o644)
		}
	}
	backgroundMu.Unlock()
	if err != nil {
		writeErr(rw, err)
		return
	}
	writeJSON(rw, http.StatusOK, w.background(r.Context()))
}

// BackgroundCollection is a set of pictures to choose from, as Cinnamon's
// Backgrounds settings shows them.
type BackgroundCollection struct {
	Name   string  `json:"name"`
	Images []Entry `json:"images"`
}

const maxCollectionImages = 1000

// handleBackgrounds lists pictures to choose from: Cinnamon's collections
// (cinnamon-background-properties/*.xml), the Pictures folder, and folders
// added in Cinnamon's Backgrounds settings.
func (w *Worker) handleBackgrounds(rw http.ResponseWriter, r *http.Request) {
	var out []BackgroundCollection
	add := func(name string, paths []string) {
		c := BackgroundCollection{Name: name, Images: []Entry{}}
		seen := map[string]bool{}
		for _, p := range paths {
			if seen[p] || len(c.Images) == maxCollectionImages {
				continue
			}
			seen[p] = true
			info, err := os.Stat(p)
			if err != nil || !info.Mode().IsRegular() {
				continue
			}
			if e := entryFor(p, info); strings.HasPrefix(e.MIME, "image/") {
				e.Path = p
				c.Images = append(c.Images, e)
			}
		}
		if len(c.Images) > 0 {
			out = append(out, c)
		}
	}

	for _, dir := range dataDirs() {
		files, _ := filepath.Glob(filepath.Join(dir, "cinnamon-background-properties", "*.xml"))
		for _, f := range files {
			add(collectionName(f), readWallpaperList(f))
		}
	}
	dirs := userDirs(w.info.Home, w.configHome)
	if pics, ok := dirs["PICTURES"]; ok && pics != w.info.Home {
		add(filepath.Base(pics), filesIn(pics))
	}
	if b, err := os.ReadFile(filepath.Join(w.info.Home, ".cinnamon", "backgrounds", "user-folders.lst")); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			if dir := strings.TrimSpace(line); filepath.IsAbs(dir) {
				add(filepath.Base(dir), filesIn(dir))
			}
		}
	}
	if out == nil {
		out = []BackgroundCollection{}
	}
	writeJSON(rw, http.StatusOK, map[string]any{"collections": out})
}

// dataDirs is $XDG_DATA_DIRS, where system data such as wallpaper lists live.
func dataDirs() []string {
	v := os.Getenv("XDG_DATA_DIRS")
	if v == "" {
		v = "/usr/local/share:/usr/share"
	}
	var out []string
	for _, d := range filepath.SplitList(v) {
		if filepath.IsAbs(d) && !slices.Contains(out, filepath.Clean(d)) {
			out = append(out, filepath.Clean(d))
		}
	}
	return out
}

// collectionName turns "linuxmint-wallpapers.xml" into "Linuxmint Wallpapers".
func collectionName(file string) string {
	words := strings.FieldsFunc(strings.TrimSuffix(filepath.Base(file), ".xml"), func(r rune) bool {
		return r == '-' || r == '_' || r == ' '
	})
	for i, w := range words {
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.Join(words, " ")
}

// readWallpaperList reads the pictures of a GNOME/Cinnamon wallpaper list.
func readWallpaperList(file string) []string {
	f, err := os.Open(file)
	if err != nil {
		return nil
	}
	defer f.Close()
	var list struct {
		Wallpapers []struct {
			Deleted  string `xml:"deleted,attr"`
			Filename string `xml:"filename"`
		} `xml:"wallpaper"`
	}
	dec := xml.NewDecoder(f)
	dec.Strict = false
	if err := dec.Decode(&list); err != nil {
		return nil
	}
	var out []string
	for _, wp := range list.Wallpapers {
		p := strings.TrimSpace(wp.Filename)
		if wp.Deleted != "true" && filepath.IsAbs(p) && !strings.HasSuffix(p, ".xml") {
			out = append(out, filepath.Clean(p))
		}
	}
	return out
}

// filesIn lists the files directly in dir, by name.
func filesIn(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	return out
}
