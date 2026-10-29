package worker

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// fakePactl answers pactl's JSON queries from files in its folder and logs
// every other command.
func fakePactl(t *testing.T) (dir string) {
	t.Helper()
	dir = t.TempDir()
	os.WriteFile(filepath.Join(dir, "info"), []byte(`{"server_name":"PulseAudio (on PipeWire 1.0.5)","default_sink_name":"speakers"}`), 0o644)
	os.WriteFile(filepath.Join(dir, "sinks"), []byte(`[
 {"index":36,"name":"speakers","description":"Built-in Audio","mute":false,
  "volume":{"front-left":{"value":32768,"value_percent":"50%"},"front-right":{"value":36000,"value_percent":"55%"}}},
 {"index":40,"name":"hdmi","description":"","mute":true,"volume":{"mono":{"value_percent":"100%"}}}
]`), 0o644)
	os.WriteFile(filepath.Join(dir, "sink-inputs"), []byte(`[
 {"index":7,"mute":false,"volume":{"front-left":{"value_percent":"80%"}},
  "properties":{"application.name":"Firefox","application.icon_name":"firefox","media.name":"Video"}}
]`), 0o644)
	script := filepath.Join(dir, "pactl")
	os.WriteFile(script, []byte(`#!/bin/sh
d=$(dirname "$0")
case "$1 $2 $3 $4" in
"-f json info "*) cat "$d/info";;
"-f json list sinks") cat "$d/sinks";;
"-f json list sink-inputs") cat "$d/sink-inputs";;
subscribe*) sleep 60;;
*) echo "$@" >> "$d/log";;
esac
`), 0o755)
	old := pactlCommand
	pactlCommand = script
	t.Cleanup(func() { pactlCommand = old })
	return dir
}

// TestSound: the outputs (the default marked, volumes averaged over the
// channels) and the apps playing; changes go to pactl as arguments, for
// listed outputs and apps only, the volume capped at 150%.
func TestSound(t *testing.T) {
	dir := fakePactl(t)
	w := driveWorker(t, nil)
	t.Cleanup(w.sound.close)
	rec := serve(t, w, "GET", "/api/v1/sound", "")
	var st SoundState
	json.Unmarshal(rec.Body.Bytes(), &st)
	want := SoundState{Available: true,
		Outputs: []SoundOutput{{"speakers", "Built-in Audio", 53, false, true}, {"hdmi", "hdmi", 100, true, false}},
		Apps:    []SoundApp{{7, "Firefox", "firefox", 80, false}}}
	if !reflect.DeepEqual(st, want) {
		t.Fatalf("got %+v\nwant %+v", st, want)
	}
	for body, code := range map[string]int{
		`{"output":"hdmi","volume":400,"muted":false,"default":true}`: http.StatusOK,
		`{"app":7,"volume":-5,"muted":true}`:                          http.StatusOK,
		`{"output":"--help","volume":5}`:                              http.StatusNotFound,
		`{"app":99,"volume":5}`:                                       http.StatusNotFound,
	} {
		if rec := serve(t, w, "POST", "/api/v1/sound", body); rec.Code != code {
			t.Errorf("%s: %d %s", body, rec.Code, rec.Body)
		}
	}
	b, _ := os.ReadFile(filepath.Join(dir, "log"))
	got := strings.Split(strings.TrimSpace(string(b)), "\n")
	wantCmds := []string{"set-sink-volume hdmi 150%", "set-sink-mute hdmi 0", "set-default-sink hdmi",
		"set-sink-input-volume 7 0%", "set-sink-input-mute 7 1"}
	slices.Sort(got)
	slices.Sort(wantCmds)
	if !reflect.DeepEqual(got, wantCmds) {
		t.Fatalf("pactl ran %q", got)
	}

	// No sound server: an empty, unavailable state.
	pactlCommand = filepath.Join(t.TempDir(), "missing")
	rec = serve(t, w, "GET", "/api/v1/sound", "")
	if !strings.Contains(rec.Body.String(), `"available":false`) {
		t.Fatalf("without a server: %s", rec.Body)
	}
}
