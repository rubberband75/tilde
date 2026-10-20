package worker

import (
	"bufio"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// Place is a sidebar shortcut (Home, Desktop, Documents, ...).
type Place struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Path string `json:"path"`
	Icon string `json:"icon"`
}

// userDirs reads the XDG user directories from ~/.config/user-dirs.dirs,
// which is where Mint keeps the (possibly translated) folder names.
func userDirs(home, configHome string) map[string]string {
	dirs := map[string]string{
		"DESKTOP": filepath.Join(home, "Desktop"),
	}
	f, err := os.Open(filepath.Join(configHome, "user-dirs.dirs"))
	if err != nil {
		return dirs
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "#") || !strings.HasPrefix(line, "XDG_") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key, ok := strings.CutSuffix(strings.TrimPrefix(k, "XDG_"), "_DIR")
		if !ok {
			continue
		}
		v = strings.Trim(v, `"`)
		v = strings.Replace(v, "$HOME", home, 1)
		if !filepath.IsAbs(v) {
			continue
		}
		dirs[key] = filepath.Clean(v)
	}
	return dirs
}

func (w *Worker) places() []Place {
	dirs := userDirs(w.info.Home, w.configHome)
	out := []Place{{ID: "home", Name: "Home", Path: w.info.Home, Icon: "user-home"}}
	for _, p := range []struct{ key, id, name, icon string }{
		{"DESKTOP", "desktop", "Desktop", "user-desktop"},
		{"DOCUMENTS", "documents", "Documents", "folder-documents"},
		{"DOWNLOAD", "downloads", "Downloads", "folder-download"},
		{"MUSIC", "music", "Music", "folder-music"},
		{"PICTURES", "pictures", "Pictures", "folder-pictures"},
		{"VIDEOS", "videos", "Videos", "folder-videos"},
	} {
		path, ok := dirs[p.key]
		if !ok || path == w.info.Home {
			continue
		}
		if st, err := os.Stat(path); err != nil || !st.IsDir() {
			continue
		}
		out = append(out, Place{ID: p.id, Name: filepath.Base(path), Path: path, Icon: p.icon})
	}
	out = append(out,
		Place{ID: "root", Name: "File System", Path: "/", Icon: "drive-harddisk"},
		Place{ID: "trash", Name: "Trash", Path: filepath.Join(w.trash.Dir(), "files"), Icon: "user-trash"},
	)
	return out
}

func (w *Worker) handlePlaces(rw http.ResponseWriter, r *http.Request) {
	writeJSON(rw, http.StatusOK, map[string]any{"places": w.places()})
}

// Mount is a mounted filesystem, for "My Computer".
type Mount struct {
	Device     string `json:"device"`
	MountPoint string `json:"mountPoint"`
	FSType     string `json:"fsType"`
	Size       uint64 `json:"size"`
	Free       uint64 `json:"free"`
}

// mounts lists real block-device filesystems from /proc/self/mounts.
// TODO: removable drives and mount/unmount through udisks2 over D-Bus.
func mounts() []Mount {
	f, err := os.Open("/proc/self/mounts")
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []Mount
	seen := map[string]bool{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 3 {
			continue
		}
		dev, mp, fstype := fields[0], unescapeMount(fields[1]), fields[2]
		if !strings.HasPrefix(dev, "/dev/") || strings.HasPrefix(dev, "/dev/loop") || seen[dev] {
			continue
		}
		if strings.HasPrefix(mp, "/snap/") || strings.HasPrefix(mp, "/boot/efi") {
			continue
		}
		seen[dev] = true
		m := Mount{Device: dev, MountPoint: mp, FSType: fstype}
		var st syscall.Statfs_t
		if syscall.Statfs(mp, &st) == nil {
			m.Size = st.Blocks * uint64(st.Bsize)
			m.Free = st.Bavail * uint64(st.Bsize)
		}
		out = append(out, m)
	}
	return out
}

// unescapeMount decodes the octal escapes (\040 for space) used in
// /proc/self/mounts.
func unescapeMount(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			n := 0
			ok := true
			for _, c := range s[i+1 : i+4] {
				if c < '0' || c > '7' {
					ok = false
					break
				}
				n = n*8 + int(c-'0')
			}
			if ok {
				b.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func osPrettyName() string {
	f, err := os.Open("/etc/os-release")
	if err != nil {
		return "Linux"
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), "PRETTY_NAME="); ok {
			return strings.Trim(v, `"`)
		}
	}
	return "Linux"
}

func (w *Worker) handleSystem(rw http.ResponseWriter, r *http.Request) {
	var uts syscall.Utsname
	kernel := ""
	if syscall.Uname(&uts) == nil {
		kernel = charsToString(uts.Release[:])
	}
	writeJSON(rw, http.StatusOK, map[string]any{
		"hostname": w.hostname,
		"os":       osPrettyName(),
		"kernel":   kernel,
		"mounts":   mounts(),
	})
}

func charsToString[T int8 | uint8](ca []T) string {
	b := make([]byte, 0, len(ca))
	for _, c := range ca {
		if c == 0 {
			break
		}
		b = append(b, byte(c))
	}
	return string(b)
}
