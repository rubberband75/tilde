package worker

import (
	"errors"
	"net/url"
	"path/filepath"
	"strings"
)

// fileURI turns an absolute path into a file:// URI escaped exactly as
// GLib's g_filename_to_uri does. The exact form matters: bookmarks written
// here must look like Nemo's, and the freedesktop thumbnail cache is keyed
// by the MD5 of this string, so a different escape means a different cache
// entry than Nemo's.
func fileURI(path string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	b.WriteString("file://")
	for i := 0; i < len(path); i++ {
		c := path[i]
		if uriSafe(c) {
			b.WriteByte(c)
		} else {
			b.WriteByte('%')
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&15])
		}
	}
	return b.String()
}

// uriSafe reports the bytes GLib leaves unescaped in a path: letters,
// digits and !$&'()*+,-./:=@_~.
func uriSafe(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	}
	return strings.IndexByte("!$&'()*+,-./:=@_~", c) >= 0
}

// pathFromURI is the inverse for file:// URIs without a host.
func pathFromURI(uri string) (string, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return "", err
	}
	if u.Scheme != "file" || (u.Host != "" && u.Host != "localhost") {
		return "", errors.New("not a local file URI")
	}
	if !filepath.IsAbs(u.Path) {
		return "", errors.New("relative file URI")
	}
	return filepath.Clean(u.Path), nil
}
