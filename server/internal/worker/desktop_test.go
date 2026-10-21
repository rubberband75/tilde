package worker

import (
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func getLayout(t *testing.T, c *http.Client) DesktopLayout {
	t.Helper()
	resp, err := c.Get(api("/api/v1/desktop/layout", nil))
	if err != nil {
		t.Fatal(err)
	}
	return decode[DesktopLayout](t, resp, http.StatusOK)
}

func TestDesktopLayoutSave(t *testing.T) {
	c, home := startWorker(t)

	l := getLayout(t, c)
	if l.Saved || len(l.Positions) != 0 || l.Nemo == nil {
		t.Fatalf("fresh layout = %+v", l)
	}

	// Saves merge, so two clients moving different icons keep both moves.
	decode[DesktopLayout](t, postJSON(t, c, "/api/v1/desktop/layout", map[string]any{
		"positions": map[string]any{"trash:///": Cell{0, 2}, "notes.txt": Cell{3, 1}},
	}), http.StatusOK)
	got := decode[DesktopLayout](t, postJSON(t, c, "/api/v1/desktop/layout", map[string]any{
		"positions": map[string]any{"notes.txt": nil, "Projects": Cell{1, 0}},
	}), http.StatusOK)
	want := map[string]Cell{"trash:///": {0, 2}, "Projects": {1, 0}}
	if !reflect.DeepEqual(got.Positions, want) {
		t.Fatalf("after merge: %+v, want %+v", got.Positions, want)
	}
	if l := getLayout(t, c); !l.Saved || l.Nemo != nil || !reflect.DeepEqual(l.Positions, want) {
		t.Fatalf("saved layout = %+v", l)
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "tilde", "desktop-layout.json")); err != nil {
		t.Fatal(err)
	}

	// Replace starts over; an empty saved layout stays saved (no Nemo seed).
	decode[DesktopLayout](t, postJSON(t, c, "/api/v1/desktop/layout", map[string]any{
		"positions": map[string]any{}, "replace": true,
	}), http.StatusOK)
	if l := getLayout(t, c); !l.Saved || len(l.Positions) != 0 || l.Nemo != nil {
		t.Fatalf("after replace: %+v", l)
	}

	for _, bad := range []map[string]any{
		{"a/b": Cell{0, 0}},
		{"..": Cell{0, 0}},
		{"": Cell{0, 0}},
		{"x": Cell{-1, 0}},
		{"x": Cell{0, maxDesktopCell + 1}},
	} {
		resp := postJSON(t, c, "/api/v1/desktop/layout", map[string]any{"positions": bad})
		decode[apiError](t, resp, http.StatusBadRequest)
	}
}

func TestDesktopLayoutSeededFromNemo(t *testing.T) {
	c, home := startWorker(t)
	write(t, filepath.Join(home, ".config", "nemo", "desktop-metadata"), `[desktop-monitor-0]
nemo-icon-view-auto-layout=true
desktop-grid-adjust=56;91;

[HDD.volume]
nemo-icon-position=86,8
monitor=0

[computer]
nemo-icon-position=32,20
monitor=0

[trash]
nemo-icon-position=32,111
monitor=0
`)
	desktop := filepath.Join(home, "Desktop")
	for _, name := range []string{"a", "b", "c d"} {
		write(t, filepath.Join(desktop, name), "")
	}
	// A stand-in for gio: "a" and "c d" are on monitor 0, "b" on monitor 1.
	fake := filepath.Join(t.TempDir(), "gio")
	write(t, fake, `#!/bin/sh
shift 3
for p in "$@"; do
  echo "uri: file://$p"
  echo "local path: $p"
  echo "attributes:"
  case "${p##*/}" in
    a) echo "  metadata::nemo-icon-position: 32,202"; echo "  metadata::monitor: 0" ;;
    b) echo "  metadata::nemo-icon-position: 900,20"; echo "  metadata::monitor: 1" ;;
    "c d") echo "  metadata::nemo-icon-position: 88,20" ;;
  esac
done
`)
	if err := os.Chmod(fake, 0o755); err != nil {
		t.Fatal(err)
	}
	old := gioCommand
	gioCommand = fake
	t.Cleanup(func() { gioCommand = old })

	l := getLayout(t, c)
	want := &NemoLayout{
		Grid: &[2]int{56, 91},
		Icons: map[string]NemoIcon{
			"computer:///": {32, 20},
			"trash:///":    {32, 111},
			"a":            {32, 202},
			"c d":          {88, 20},
		},
	}
	if !reflect.DeepEqual(l.Nemo, want) {
		t.Fatalf("nemo = %+v (grid %v), want %+v", l.Nemo, l.Nemo.Grid, want)
	}

	// Without gio (or Nemo) there is simply nothing to seed from.
	gioCommand = filepath.Join(t.TempDir(), "missing")
	if l := getLayout(t, c); len(l.Nemo.Icons) != 2 {
		t.Fatalf("without gio: %+v", l.Nemo.Icons)
	}
}
