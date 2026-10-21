package worker

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func writePNG(t *testing.T, path string, w, h int) {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.NRGBA{uint8(x), uint8(y), 200, 255})
		}
	}
	var buf bytes.Buffer
	png.Encode(&buf, img)
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func getThumb(t *testing.T, c *http.Client, path, size string) (int, []byte) {
	t.Helper()
	resp, err := c.Get(api("/api/v1/fs/thumbnail", url.Values{"path": {path}, "size": {size}}))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func cachePath(home, flavor, path string) string {
	sum := md5.Sum([]byte(fileURI(path)))
	return filepath.Join(home, ".cache", "thumbnails", flavor, hex.EncodeToString(sum[:])+".png")
}

func TestNativeThumbnail(t *testing.T) {
	c, home := startWorker(t)
	pic := filepath.Join(home, "My Pictures", "wide.png")
	os.MkdirAll(filepath.Dir(pic), 0o755)
	writePNG(t, pic, 600, 300)

	resp, _ := c.Get(api("/api/v1/fs/list", url.Values{"path": {filepath.Dir(pic)}}))
	if l := decode[ListResponse](t, resp, http.StatusOK); !l.Entries[0].Thumb {
		t.Fatal("a PNG should be marked as thumbnailable")
	}

	status, data := getThumb(t, c, pic, "normal")
	if status != http.StatusOK {
		t.Fatalf("status %d: %s", status, data)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil || img.Bounds().Dx() != 128 || img.Bounds().Dy() != 64 {
		t.Fatalf("thumbnail %v, %v", img.Bounds(), err)
	}
	st, _ := os.Stat(pic)
	meta := pngText(data)
	if meta["Thumb::URI"] != "file://"+strings.ReplaceAll(pic, " ", "%20") || meta["Thumb::MTime"] != strconv.FormatInt(st.ModTime().Unix(), 10) {
		t.Fatalf("metadata %v", meta)
	}
	cached := cachePath(home, "normal", pic)
	cst, err := os.Stat(cached)
	if err != nil || cst.Mode().Perm() != 0o600 {
		t.Fatalf("cache file %v, %v", cst, err)
	}
	if dst, _ := os.Stat(filepath.Dir(cached)); dst.Mode().Perm() != 0o700 {
		t.Fatalf("cache dir mode %v", dst.Mode())
	}

	// Served from the cache until the file changes.
	os.WriteFile(cached, append(append([]byte{}, data...), 0), 0o600) // marker byte
	if _, again := getThumb(t, c, pic, "normal"); len(again) != len(data)+1 {
		t.Fatal("the cached thumbnail was not reused")
	}
	later := time.Now().Add(time.Hour)
	os.Chtimes(pic, later, later)
	if _, fresh := getThumb(t, c, pic, "normal"); len(fresh) == len(data)+1 {
		t.Fatal("a changed file kept its old thumbnail")
	}
	if _, large := getThumb(t, c, pic, "large"); pngSize(t, large) != [2]int{256, 128} {
		t.Fatal("large thumbnail has the wrong size")
	}
}

func pngSize(t *testing.T, data []byte) [2]int {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return [2]int{img.Bounds().Dx(), img.Bounds().Dy()}
}

func TestThumbnailerProgram(t *testing.T) {
	c, home := startWorker(t)
	dir := t.TempDir()
	sample := filepath.Join(dir, "sample.png")
	writePNG(t, sample, 64, 64)
	runs := filepath.Join(dir, "runs")
	script := filepath.Join(dir, "fake-thumbnailer")
	os.WriteFile(script, []byte("#!/bin/sh\necho run >> "+runs+"\ncase \"$1\" in *bad*) exit 1;; esac\ncp "+sample+" \"$2\"\n"), 0o755)
	os.WriteFile(filepath.Join(dir, "fake.thumbnailer"),
		[]byte("[Thumbnailer Entry]\nTryExec="+script+"\nExec="+script+" %i %o %s\nMimeType=text/plain;\n"), 0o644)
	setThumbnailers(loadThumbnailers([]string{dir}))
	defer setThumbnailers(&thumbRegistry{byMIME: map[string]thumbnailer{}})

	good := filepath.Join(home, "good.txt")
	bad := filepath.Join(home, "bad.txt")
	write(t, good, "hello")
	write(t, bad, "hello")
	if status, data := getThumb(t, c, good, "normal"); status != http.StatusOK || pngText(data)["Thumb::URI"] != fileURI(good) {
		t.Fatalf("good: %d %v", status, pngText(data))
	}
	// A failure is remembered and not retried.
	for i := 0; i < 2; i++ {
		if status, _ := getThumb(t, c, bad, "normal"); status != http.StatusNotFound {
			t.Fatalf("bad: status %d", status)
		}
	}
	if n := strings.Count(read(t, runs), "run"); n != 2 {
		t.Fatalf("thumbnailer ran %d times, want 2 (good once, bad once)", n)
	}
	// Files in a thumbnail cache are never thumbnailed.
	inCache := filepath.Join(home, ".cache", "thumbnails", "x.txt")
	write(t, inCache, "x")
	if status, _ := getThumb(t, c, inCache, "normal"); status != http.StatusNotFound {
		t.Fatalf("cache file: %d", status)
	}
}

func TestPNGText(t *testing.T) {
	var buf bytes.Buffer
	png.Encode(&buf, image.NewNRGBA(image.Rect(0, 0, 2, 2)))
	out, err := addPNGText(buf.Bytes(), [][2]string{{"Thumb::URI", "file:///a%20b"}, {"Thumb::MTime", "42"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := png.Decode(bytes.NewReader(out)); err != nil {
		t.Fatalf("result is not a valid PNG: %v", err)
	}
	if m := pngText(out); m["Thumb::URI"] != "file:///a%20b" || m["Thumb::MTime"] != "42" {
		t.Fatalf("text = %v", m)
	}
	if _, err := addPNGText([]byte("GIF89a"), nil); err == nil {
		t.Fatal("non-PNG accepted")
	}
}
