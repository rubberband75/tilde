package worker

import (
	"bytes"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// sniffBudget is how many files one directory listing may open to sniff.
const sniffBudget = 1000

// mimeFor returns the MIME type of a file. On Linux, Go's mime package reads
// the shared-mime-info globs (/usr/share/mime/globs2), so for names with an
// extension this mostly agrees with Nemo. Regular files the name says
// nothing about are sniffed from their first bytes, unless budget (shared by
// a listing) has run out.
func mimeFor(path, name string, info fs.FileInfo, broken bool, budget *int) string {
	switch {
	case broken:
		return "inode/symlink"
	case info.IsDir():
		return "inode/directory"
	case !info.Mode().IsRegular():
		return "inode/x-special"
	}
	if ext := filepath.Ext(name); ext != "" {
		if t := mime.TypeByExtension(strings.ToLower(ext)); t != "" {
			return stripParams(t)
		}
	}
	if budget == nil || *budget > 0 {
		if budget != nil {
			*budget--
		}
		if t := sniff(path, info); t != "" {
			return t
		}
	}
	if info.Mode().Perm()&0o111 != 0 {
		return "application/x-executable"
	}
	return "application/octet-stream"
}

// sniff guesses a type from content: ELF binaries, scripts by their #!
// interpreter, empty files, then Go's content sniffer (text, HTML, images,
// PDF, archives). It returns "" when it cannot tell.
func sniff(path string, info fs.FileInfo) string {
	if info.Size() == 0 {
		return "application/x-zerosize"
	}
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	buf := make([]byte, 512)
	n, err := io.ReadFull(f, buf)
	if err != nil && err != io.ErrUnexpectedEOF {
		return ""
	}
	buf = buf[:n]

	if bytes.HasPrefix(buf, []byte("\x7fELF")) && len(buf) > 17 {
		// e_type: 2 is an executable, 3 a shared object (which is also
		// what position-independent executables are).
		switch buf[16] {
		case 2:
			return "application/x-executable"
		case 3:
			if info.Mode().Perm()&0o111 != 0 {
				return "application/x-executable"
			}
			return "application/x-sharedlib"
		}
		return "application/x-executable"
	}
	if bytes.HasPrefix(buf, []byte("#!")) {
		return scriptType(buf)
	}
	t := stripParams(http.DetectContentType(buf))
	if t == "application/octet-stream" {
		return ""
	}
	return t
}

// scriptType maps a #! line to the MIME type shared-mime-info uses.
func scriptType(buf []byte) string {
	line, _, _ := bytes.Cut(buf[2:], []byte("\n"))
	fields := strings.Fields(string(line))
	if len(fields) == 0 {
		return "text/plain"
	}
	interp := filepath.Base(fields[0])
	if interp == "env" && len(fields) > 1 {
		interp = fields[1]
		if interp == "-S" && len(fields) > 2 {
			interp = fields[2]
		}
		interp = filepath.Base(interp)
	}
	switch {
	case strings.HasPrefix(interp, "python"):
		return "text/x-python"
	case strings.HasPrefix(interp, "perl"):
		return "application/x-perl"
	case strings.HasPrefix(interp, "ruby"):
		return "application/x-ruby"
	case interp == "node" || interp == "nodejs" || interp == "deno":
		return "application/javascript"
	case interp == "php":
		return "application/x-php"
	}
	return "application/x-shellscript"
}

func stripParams(t string) string {
	if i := strings.IndexByte(t, ';'); i >= 0 {
		t = t[:i]
	}
	return strings.TrimSpace(t)
}
