package worker

import (
	"bufio"
	"bytes"
	"context"
	"crypto/md5"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/crc32"
	"image"
	"image/draw"
	_ "image/gif" // registered for the built-in thumbnailer
	_ "image/jpeg"
	"image/png"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Thumbnails follow the freedesktop thumbnail spec
// (https://specifications.freedesktop.org/thumbnail-spec/), so they share
// ~/.cache/thumbnails with Nemo: a PNG named after the MD5 of the file's URI,
// tagged with Thumb::URI and Thumb::MTime, and reused while the mtime
// matches. They are made by the host's thumbnailers
// (/usr/share/thumbnailers/*.thumbnailer, as Nemo uses), or for PNG, JPEG and
// GIF by a built-in scaler when no thumbnailer is installed. Thumbnailers run
// as the user on files that may be hostile, exactly as under Nemo, with a
// timeout and at most two at a time.

const (
	thumbNormal = 128
	thumbLarge  = 256
	// maxThumbSource caps the file size thumbnailed (Nemo's default limit
	// is similar); maxNativePixels guards the built-in decoder.
	maxThumbSource  = 1 << 30
	maxNativeSource = 64 << 20
	maxNativePixels = 40_000_000
	thumbTimeout    = 20 * time.Second
	failDir         = "fail/tilde-1"
)

var nativeThumbTypes = map[string]bool{"image/png": true, "image/jpeg": true, "image/gif": true}

// thumbnailer is one *.thumbnailer file.
type thumbnailer struct {
	exec []string
}

type thumbRegistry struct {
	byMIME map[string]thumbnailer
}

var (
	thumbsMu sync.RWMutex
	thumbs   = &thumbRegistry{byMIME: map[string]thumbnailer{}}
	// thumbSem limits concurrent thumbnail generation.
	thumbSem = make(chan struct{}, 2)
)

// loadThumbnailers reads the thumbnailer files in dirs; earlier dirs win.
func loadThumbnailers(dirs []string) *thumbRegistry {
	reg := &thumbRegistry{byMIME: map[string]thumbnailer{}}
	for i := len(dirs) - 1; i >= 0; i-- {
		files, _ := filepath.Glob(filepath.Join(dirs[i], "*.thumbnailer"))
		for _, f := range files {
			t, mimes, ok := parseThumbnailer(f)
			if !ok {
				continue
			}
			for _, m := range mimes {
				reg.byMIME[m] = t
			}
		}
	}
	return reg
}

func parseThumbnailer(path string) (thumbnailer, []string, bool) {
	f, err := os.Open(path)
	if err != nil {
		return thumbnailer{}, nil, false
	}
	defer f.Close()
	var execLine, tryExec string
	var mimes []string
	inEntry := false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "[") {
			inEntry = line == "[Thumbnailer Entry]"
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !inEntry || !ok {
			continue
		}
		switch strings.TrimSpace(k) {
		case "Exec":
			execLine = strings.TrimSpace(v)
		case "TryExec":
			tryExec = strings.TrimSpace(v)
		case "MimeType":
			for _, m := range strings.Split(v, ";") {
				if m = strings.TrimSpace(m); m != "" {
					mimes = append(mimes, m)
				}
			}
		}
	}
	args := strings.Fields(execLine)
	if len(args) == 0 || len(mimes) == 0 {
		return thumbnailer{}, nil, false
	}
	// Skip thumbnailers whose program is not installed.
	prog := args[0]
	if tryExec != "" {
		prog = tryExec
	}
	if _, err := exec.LookPath(prog); err != nil {
		return thumbnailer{}, nil, false
	}
	return thumbnailer{exec: args}, mimes, true
}

func thumbnailerDirs(dataHome string) []string {
	dirs := []string{filepath.Join(dataHome, "thumbnailers")}
	dataDirs := os.Getenv("XDG_DATA_DIRS")
	if dataDirs == "" {
		dataDirs = "/usr/local/share:/usr/share"
	}
	for _, d := range strings.Split(dataDirs, ":") {
		if filepath.IsAbs(d) {
			dirs = append(dirs, filepath.Join(d, "thumbnailers"))
		}
	}
	return dirs
}

func setThumbnailers(reg *thumbRegistry) {
	thumbsMu.Lock()
	thumbs = reg
	thumbsMu.Unlock()
}

// canThumbnail reports whether a file of this type and size can get a
// thumbnail, for Entry.Thumb. Files in a thumbnail cache never do.
func canThumbnail(path, mime string, info fs.FileInfo) bool {
	if !info.Mode().IsRegular() || info.Size() == 0 || info.Size() > maxThumbSource {
		return false
	}
	if strings.Contains(path, "/.cache/thumbnails/") {
		return false
	}
	thumbsMu.RLock()
	_, ok := thumbs.byMIME[mime]
	thumbsMu.RUnlock()
	return ok || (nativeThumbTypes[mime] && info.Size() <= maxNativeSource)
}

func (w *Worker) handleThumbnail(rw http.ResponseWriter, r *http.Request) {
	p, err := queryPath(r, "path")
	if err != nil {
		writeErr(rw, err)
		return
	}
	size, flavor := thumbNormal, "normal"
	if r.URL.Query().Get("size") == "large" {
		size, flavor = thumbLarge, "large"
	}
	st, err := os.Stat(p)
	if err != nil {
		writeErr(rw, err)
		return
	}
	mime := mimeFor(p, filepath.Base(p), st, false, nil)
	if !canThumbnail(p, mime, st) {
		writeJSON(rw, http.StatusNotFound, apiError{Error: "no thumbnail for this file", Code: "no_thumbnail"})
		return
	}
	uri := fileURI(p)
	sum := md5.Sum([]byte(uri))
	name := hex.EncodeToString(sum[:]) + ".png"
	cached := filepath.Join(w.cacheHome, "thumbnails", flavor, name)
	failed := filepath.Join(w.cacheHome, "thumbnails", failDir, name)
	mtime := strconv.FormatInt(st.ModTime().Unix(), 10)

	data, ok := validThumb(cached, uri, mtime)
	if !ok {
		if _, isFail := validThumb(failed, uri, mtime); isFail {
			writeJSON(rw, http.StatusNotFound, apiError{Error: "thumbnailing this file failed before", Code: "no_thumbnail"})
			return
		}
		data, err = w.makeThumb(r.Context(), p, uri, mime, size, mtime)
		if err != nil {
			if r.Context().Err() == nil {
				// Remember the failure, as the spec asks, so the file is not
				// retried on every view; a new mtime retries.
				if marker, merr := failMarker(uri, mtime); merr == nil {
					writeCacheFile(failed, marker)
				}
			}
			writeJSON(rw, http.StatusNotFound, apiError{Error: "could not make a thumbnail: " + err.Error(), Code: "no_thumbnail"})
			return
		}
		writeCacheFile(cached, data)
	}
	h := rw.Header()
	h.Set("Content-Type", "image/png")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "sandbox; default-src 'none'")
	// The URL carries the file's mtime, so a changed file gets a new URL.
	h.Set("Cache-Control", "private, max-age=86400")
	rw.Write(data)
}

// makeThumb runs a thumbnailer (or the built-in scaler) and returns the PNG
// tagged with the spec's metadata.
func (w *Worker) makeThumb(ctx context.Context, path, uri, mime string, size int, mtime string) ([]byte, error) {
	select {
	case thumbSem <- struct{}{}:
		defer func() { <-thumbSem }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	thumbsMu.RLock()
	t, ok := thumbs.byMIME[mime]
	thumbsMu.RUnlock()
	var out []byte
	var err error
	if ok {
		out, err = runThumbnailer(ctx, t, path, uri, size)
	} else {
		out, err = nativeThumb(path, size)
	}
	if err != nil {
		return nil, err
	}
	return addPNGText(out, [][2]string{
		{"Thumb::URI", uri},
		{"Thumb::MTime", mtime},
		{"Software", "Tilde"},
	})
}

func runThumbnailer(ctx context.Context, t thumbnailer, path, uri string, size int) ([]byte, error) {
	tmp, err := os.CreateTemp("", "tilde-thumb-*.png")
	if err != nil {
		return nil, err
	}
	tmp.Close()
	defer os.Remove(tmp.Name())
	args := make([]string, 0, len(t.exec))
	for _, a := range t.exec {
		a = strings.NewReplacer("%i", path, "%u", uri, "%o", tmp.Name(), "%s", strconv.Itoa(size), "%%", "%").Replace(a)
		args = append(args, a)
	}
	ctx, cancel := context.WithTimeout(ctx, thumbTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	// Its own process group, so a timeout also stops what it started.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(args[0]), err)
	}
	data, err := os.ReadFile(tmp.Name())
	if err != nil {
		return nil, err
	}
	if !bytes.HasPrefix(data, pngSignature) {
		return nil, errors.New("the thumbnailer did not write a PNG")
	}
	return data, nil
}

// nativeThumb scales PNG, JPEG and GIF images with Go's decoders, for hosts
// without a thumbnailer for them. (EXIF orientation is not applied.)
func nativeThumb(path string, size int) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return nil, err
	}
	if cfg.Width*cfg.Height > maxNativePixels {
		return nil, errors.New("image too large")
	}
	if _, err := f.Seek(0, 0); err != nil {
		return nil, err
	}
	src, _, err := image.Decode(f)
	if err != nil {
		return nil, err
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w > size || h > size {
		if w >= h {
			w, h = size, max(1, h*size/w)
		} else {
			w, h = max(1, w*size/h), size
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, scaleImage(src, w, h)); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// scaleImage shrinks src to w×h by averaging the source pixels under each
// target pixel (a box filter: good for large reductions, and simple).
func scaleImage(src image.Image, w, h int) *image.NRGBA {
	b := src.Bounds()
	rgba := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(rgba, rgba.Bounds(), src, b.Min, draw.Src)
	sw, sh := b.Dx(), b.Dy()
	dst := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		y0, y1 := y*sh/h, max(y*sh/h+1, (y+1)*sh/h)
		for x := 0; x < w; x++ {
			x0, x1 := x*sw/w, max(x*sw/w+1, (x+1)*sw/w)
			var r, g, bl, a, n uint64
			for sy := y0; sy < y1; sy++ {
				row := rgba.Pix[sy*rgba.Stride:]
				for sx := x0; sx < x1; sx++ {
					p := row[sx*4 : sx*4+4]
					r += uint64(p[0])
					g += uint64(p[1])
					bl += uint64(p[2])
					a += uint64(p[3])
					n++
				}
			}
			o := dst.PixOffset(x, y)
			if a == 0 {
				continue
			}
			// rgba holds premultiplied colors; NRGBA wants them straight.
			dst.Pix[o] = uint8(r * 255 / a)
			dst.Pix[o+1] = uint8(g * 255 / a)
			dst.Pix[o+2] = uint8(bl * 255 / a)
			dst.Pix[o+3] = uint8(a / n)
		}
	}
	return dst
}

var pngSignature = []byte("\x89PNG\r\n\x1a\n")

// addPNGText inserts tEXt chunks right after the IHDR chunk.
func addPNGText(data []byte, kv [][2]string) ([]byte, error) {
	if !bytes.HasPrefix(data, pngSignature) || len(data) < 33 || string(data[12:16]) != "IHDR" {
		return nil, errors.New("not a PNG")
	}
	var chunks bytes.Buffer
	for _, p := range kv {
		body := append([]byte("tEXt"+p[0]+"\x00"), p[1]...)
		binary.Write(&chunks, binary.BigEndian, uint32(len(body)-4))
		chunks.Write(body)
		binary.Write(&chunks, binary.BigEndian, crc32.ChecksumIEEE(body))
	}
	out := make([]byte, 0, len(data)+chunks.Len())
	out = append(out, data[:33]...)
	out = append(out, chunks.Bytes()...)
	return append(out, data[33:]...), nil
}

// pngText reads the tEXt chunks before the image data.
func pngText(data []byte) map[string]string {
	out := map[string]string{}
	if !bytes.HasPrefix(data, pngSignature) {
		return out
	}
	for i := 8; i+12 <= len(data); {
		n := int(binary.BigEndian.Uint32(data[i:]))
		typ := string(data[i+4 : i+8])
		if typ == "IDAT" || i+12+n > len(data) {
			break
		}
		if typ == "tEXt" {
			if k, v, ok := strings.Cut(string(data[i+8:i+8+n]), "\x00"); ok {
				out[k] = v
			}
		}
		i += 12 + n
	}
	return out
}

// validThumb returns a cached thumbnail if it is for uri at this mtime.
func validThumb(file, uri, mtime string) ([]byte, bool) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, false
	}
	t := pngText(data)
	return data, t["Thumb::URI"] == uri && t["Thumb::MTime"] == mtime
}

// failMarker is the 1×1 PNG the spec stores for files that failed.
func failMarker(uri, mtime string) ([]byte, error) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewNRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		return nil, err
	}
	return addPNGText(buf.Bytes(), [][2]string{{"Thumb::URI", uri}, {"Thumb::MTime", mtime}})
}

// writeCacheFile writes atomically with the permissions the spec asks for
// (0700 folders, 0600 files: thumbnails reveal file contents).
func writeCacheFile(file string, data []byte) {
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(file), ".tilde-thumb-*")
	if err != nil {
		return
	}
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if werr != nil || cerr != nil {
		os.Remove(tmp.Name())
		return
	}
	if err := os.Rename(tmp.Name(), file); err != nil {
		os.Remove(tmp.Name())
	}
}
