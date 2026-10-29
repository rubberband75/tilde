package worker

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The sound applet (D55, D79): the host's outputs, their volume and the
// default, and the apps playing, through the user's sound server's
// PulseAudio interface (PipeWire's pipewire-pulse, or PulseAudio) with
// pactl, as the user. Without a sound server (a headless server), the
// applet is hidden.

// pactlCommand is pactl; tests replace it.
var pactlCommand = "pactl"

// maxVolume is Cinnamon's limit with amplification on: 150%.
const maxVolume = 150

// SoundOutput is a sink: speakers, headphones, HDMI.
type SoundOutput struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	// Volume in percent, the channels' average.
	Volume  int  `json:"volume"`
	Muted   bool `json:"muted"`
	Default bool `json:"default"`
}

// SoundApp is a stream playing to an output (a sink input).
type SoundApp struct {
	ID     int    `json:"id"`
	Name   string `json:"name"`
	Icon   string `json:"icon,omitempty"`
	Volume int    `json:"volume"`
	Muted  bool   `json:"muted"`
}

// SoundState is the applet's snapshot.
type SoundState struct {
	Available bool          `json:"available"`
	Outputs   []SoundOutput `json:"outputs"`
	Apps      []SoundApp    `json:"apps"`
}

// pactlVolume is pactl's JSON for a volume: per channel, "value_percent".
type pactlVolume map[string]struct {
	ValuePercent string `json:"value_percent"`
}

func (v pactlVolume) percent() int {
	if len(v) == 0 {
		return 0
	}
	sum := 0
	for _, ch := range v {
		n, _ := strconv.Atoi(strings.TrimSuffix(strings.TrimSpace(ch.ValuePercent), "%"))
		sum += n
	}
	return (sum + len(v)/2) / len(v)
}

func pactl(ctx context.Context, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, pactlCommand, args...).Output()
}

// readSound asks the sound server for its outputs and streams.
func readSound(ctx context.Context) (SoundState, error) {
	info, err := pactl(ctx, "-f", "json", "info")
	if err != nil {
		return SoundState{}, err
	}
	var server struct {
		DefaultSink string `json:"default_sink_name"`
	}
	if err := json.Unmarshal(info, &server); err != nil {
		return SoundState{}, err
	}
	sinks, err := pactl(ctx, "-f", "json", "list", "sinks")
	if err != nil {
		return SoundState{}, err
	}
	var rawSinks []struct {
		Name        string      `json:"name"`
		Description string      `json:"description"`
		Mute        bool        `json:"mute"`
		Volume      pactlVolume `json:"volume"`
	}
	if err := json.Unmarshal(sinks, &rawSinks); err != nil {
		return SoundState{}, err
	}
	st := SoundState{Available: true, Outputs: []SoundOutput{}, Apps: []SoundApp{}}
	for _, s := range rawSinks {
		st.Outputs = append(st.Outputs, SoundOutput{Name: s.Name, Description: firstNonEmpty(s.Description, s.Name),
			Volume: s.Volume.percent(), Muted: s.Mute, Default: s.Name == server.DefaultSink})
	}
	if inputs, err := pactl(ctx, "-f", "json", "list", "sink-inputs"); err == nil {
		var raw []struct {
			Index      int               `json:"index"`
			Mute       bool              `json:"mute"`
			Volume     pactlVolume       `json:"volume"`
			Properties map[string]string `json:"properties"`
		}
		if json.Unmarshal(inputs, &raw) == nil {
			for _, in := range raw {
				name := firstNonEmpty(in.Properties["application.name"], in.Properties["media.name"], "Unknown")
				st.Apps = append(st.Apps, SoundApp{ID: in.Index, Name: clip(name, 128), Icon: in.Properties["application.icon_name"],
					Volume: in.Volume.percent(), Muted: in.Mute})
			}
		}
	}
	return st, nil
}

// soundService follows the sound server with pactl subscribe, while
// someone has asked for the state, and publishes "sound" on changes.
type soundService struct {
	publish func()
	mu      sync.Mutex
	cancel  context.CancelFunc
	closed  bool
}

func newSoundService(publish func()) *soundService { return &soundService{publish: publish} }

// watch starts following the server, once.
func (s *soundService) watch() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil || s.closed {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	go s.follow(ctx)
}

func (s *soundService) follow(ctx context.Context) {
	for ctx.Err() == nil {
		cmd := exec.CommandContext(ctx, pactlCommand, "subscribe")
		out, err := cmd.StdoutPipe()
		if err == nil && cmd.Start() == nil {
			changes := make(chan struct{}, 1)
			go func() {
				// A burst of events (a volume drag) is one "sound" event.
				for range changes {
					time.Sleep(150 * time.Millisecond)
					select {
					case <-changes:
					default:
					}
					s.publish()
				}
			}()
			sc := bufio.NewScanner(out)
			for sc.Scan() {
				if line := sc.Text(); strings.Contains(line, " on sink") || strings.Contains(line, " on server") ||
					strings.Contains(line, " on card") {
					select {
					case changes <- struct{}{}:
					default:
					}
				}
			}
			cmd.Wait()
			close(changes)
		}
		// The server went away (or is not there): try again later.
		select {
		case <-ctx.Done():
		case <-time.After(10 * time.Second):
		}
	}
}

func (s *soundService) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	if s.cancel != nil {
		s.cancel()
	}
}

func (w *Worker) handleSound(rw http.ResponseWriter, r *http.Request) {
	st, err := readSound(r.Context())
	if err != nil {
		writeJSON(rw, http.StatusOK, SoundState{Outputs: []SoundOutput{}, Apps: []SoundApp{}})
		return
	}
	w.sound.watch()
	writeJSON(rw, http.StatusOK, st)
}

var errNoSuchSound = errors.New("no such output or app")

// handleSoundSet changes an output (volume, mute, made the default) or an
// app's stream (volume, mute). Only names and ids the server listed just
// now are passed to pactl, as arguments of their own.
func (w *Worker) handleSoundSet(rw http.ResponseWriter, r *http.Request) {
	var req struct {
		Output  string `json:"output"`
		App     *int   `json:"app"`
		Volume  *int   `json:"volume"`
		Muted   *bool  `json:"muted"`
		Default bool   `json:"default"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(rw, err)
		return
	}
	st, err := readSound(r.Context())
	if err != nil {
		writeJSON(rw, http.StatusNotFound, apiError{Error: "no sound server", Code: "not_found"})
		return
	}
	var cmds [][]string
	switch {
	case req.Output != "" && slices.ContainsFunc(st.Outputs, func(o SoundOutput) bool { return o.Name == req.Output }):
		if req.Volume != nil {
			cmds = append(cmds, []string{"set-sink-volume", req.Output, strconv.Itoa(clampVolume(*req.Volume)) + "%"})
		}
		if req.Muted != nil {
			cmds = append(cmds, []string{"set-sink-mute", req.Output, boolArg(*req.Muted)})
		}
		if req.Default {
			cmds = append(cmds, []string{"set-default-sink", req.Output})
		}
	case req.App != nil && slices.ContainsFunc(st.Apps, func(a SoundApp) bool { return a.ID == *req.App }):
		id := strconv.Itoa(*req.App)
		if req.Volume != nil {
			cmds = append(cmds, []string{"set-sink-input-volume", id, strconv.Itoa(clampVolume(*req.Volume)) + "%"})
		}
		if req.Muted != nil {
			cmds = append(cmds, []string{"set-sink-input-mute", id, boolArg(*req.Muted)})
		}
	default:
		writeJSON(rw, http.StatusNotFound, apiError{Error: errNoSuchSound.Error(), Code: "not_found"})
		return
	}
	for _, c := range cmds {
		if out, err := pactl(r.Context(), c...); err != nil {
			slog.Info("worker: pactl", "args", c, "err", err, "out", string(out))
			writeJSON(rw, http.StatusBadGateway, apiError{Error: "the sound server refused: " + err.Error(), Code: "sound_failed"})
			return
		}
	}
	st, _ = readSound(r.Context())
	writeJSON(rw, http.StatusOK, st)
}

func clampVolume(v int) int { return max(0, min(v, maxVolume)) }

func boolArg(b bool) string {
	if b {
		return "1"
	}
	return "0"
}
