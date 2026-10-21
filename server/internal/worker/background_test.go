package worker

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fakeCommand(t *testing.T, name, script string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	write(t, p, "#!/bin/sh\n"+script)
	if err := os.Chmod(p, 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func getBackground(t *testing.T, c *http.Client) Background {
	t.Helper()
	resp, err := c.Get(api("/api/v1/desktop/background", nil))
	if err != nil {
		t.Fatal(err)
	}
	return decode[Background](t, resp, http.StatusOK)
}

func putJSON(t *testing.T, c *http.Client, path string, body string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPut, api(path, nil), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestBackgroundFollowsCinnamonUntilSet(t *testing.T) {
	c, home := startWorker(t)
	old := gsettingsCommand
	t.Cleanup(func() { gsettingsCommand = old })

	gsettingsCommand = filepath.Join(t.TempDir(), "missing")
	if b := getBackground(t, c); b != defaultBackground() {
		t.Fatalf("without gsettings: %+v", b)
	}

	gsettingsCommand = fakeCommand(t, "gsettings", `cat <<'EOF'
org.cinnamon.desktop.background color-shading-type 'vertical'
org.cinnamon.desktop.background picture-opacity 100
org.cinnamon.desktop.background picture-options 'scaled'
org.cinnamon.desktop.background picture-uri 'file:///usr/share/backgrounds/it%27s%20here.jpg'
org.cinnamon.desktop.background primary-color '#1A2b3C'
org.cinnamon.desktop.background secondary-color '#ffff00008000'
EOF
`)
	want := Background{
		Source: "cinnamon", Picture: "/usr/share/backgrounds/it's here.jpg", Options: "scaled",
		Primary: "#1a2b3c", Secondary: "#ff0080", Shading: "vertical",
	}
	if b := getBackground(t, c); b != want {
		t.Fatalf("cinnamon: %+v, want %+v", b, want)
	}

	// Picked in Tilde: saved as Tilde's own, Cinnamon is not touched.
	pic := filepath.Join(home, "Pictures", "a.png")
	got := decode[Background](t, putJSON(t, c, "/api/v1/desktop/background",
		`{"picture":"`+pic+`","options":"bogus","primary":"red","secondary":"#00ff00","shading":"horizontal"}`), http.StatusOK)
	want2 := Background{Source: "tilde", Picture: pic, Options: "zoom", Primary: "#000000", Secondary: "#00ff00", Shading: "horizontal"}
	if got != want2 || getBackground(t, c) != want2 {
		t.Fatalf("after set: %+v, want %+v", got, want2)
	}

	decode[apiError](t, putJSON(t, c, "/api/v1/desktop/background", `{"picture":"relative.png"}`), http.StatusBadRequest)

	if got := decode[Background](t, putJSON(t, c, "/api/v1/desktop/background", `{"reset":true}`), http.StatusOK); got != want {
		t.Fatalf("after reset: %+v", got)
	}
}

func TestBackgroundCollections(t *testing.T) {
	c, home := startWorker(t)
	data := t.TempDir()
	t.Setenv("XDG_DATA_DIRS", data)
	walls := filepath.Join(data, "backgrounds")
	for _, n := range []string{"one.png", "gone.png", "two.jpg"} {
		write(t, filepath.Join(walls, n), "x")
	}
	os.Remove(filepath.Join(walls, "gone.png"))
	write(t, filepath.Join(data, "cinnamon-background-properties", "mint-set.xml"), `<?xml version="1.0"?>
<!DOCTYPE wallpapers SYSTEM "cinnamon-wp-list.dtd">
<wallpapers>
<wallpaper deleted="false"><name>One</name><filename>`+walls+`/one.png</filename></wallpaper>
<wallpaper deleted="false"><name>Gone</name><filename>`+walls+`/gone.png</filename></wallpaper>
<wallpaper deleted="true"><name>Two</name><filename>`+walls+`/two.jpg</filename></wallpaper>
<wallpaper><name>Show</name><filename>`+walls+`/show.xml</filename></wallpaper>
</wallpapers>`)
	write(t, filepath.Join(home, ".config", "user-dirs.dirs"), `XDG_PICTURES_DIR="$HOME/Bilder"`+"\n")
	write(t, filepath.Join(home, "Bilder", "cat.jpg"), "x")
	write(t, filepath.Join(home, "Bilder", "notes.txt"), "x")
	write(t, filepath.Join(home, "Bilder", ".hidden.png"), "x")
	write(t, filepath.Join(home, ".cinnamon", "backgrounds", "user-folders.lst"), walls+"\nnot/absolute\n")

	resp, err := c.Get(api("/api/v1/desktop/backgrounds", nil))
	if err != nil {
		t.Fatal(err)
	}
	got := decode[struct{ Collections []BackgroundCollection }](t, resp, http.StatusOK)
	summary := map[string][]string{}
	for _, col := range got.Collections {
		for _, e := range col.Images {
			summary[col.Name] = append(summary[col.Name], e.Name)
		}
	}
	want := map[string][]string{
		"Mint Set":    {"one.png"},
		"Bilder":      {"cat.jpg"},
		"backgrounds": {"one.png", "two.jpg"},
	}
	if len(summary) != len(want) {
		t.Fatalf("collections = %v, want %v", summary, want)
	}
	for k, v := range want {
		if len(summary[k]) != len(v) || summary[k][0] != v[0] || summary[k][len(v)-1] != v[len(v)-1] {
			t.Fatalf("collections = %v, want %v", summary, want)
		}
	}
}

func TestGVariantString(t *testing.T) {
	for in, want := range map[string]string{
		`'plain'`:         "plain",
		`'it\'s'`:         "it's",
		`"double"`:        "double",
		`100`:             "100",
		`''`:              "",
		`'back\\slash'`:   `back\slash`,
		`'unterminated`:   `'unterminated`,
		` 'trim spaces' `: "trim spaces",
	} {
		if got := gvariantString(in); got != want {
			t.Errorf("gvariantString(%s) = %q, want %q", in, got, want)
		}
	}
}
