package worker

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// driveTrash sets up a home trash and a fake second drive: paths under
// drive report another device, the way a USB stick or partition would.
func driveTrash(t *testing.T) (tr *Trash, home, drive string) {
	t.Helper()
	root := t.TempDir()
	home = filepath.Join(root, "home")
	drive = filepath.Join(root, "drive")
	for _, d := range []string{home, drive} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	tr = NewTrash(filepath.Join(home, ".local", "share"))
	tr.devOf = func(p string) (uint64, error) {
		if _, err := os.Stat(p); err != nil {
			return 0, err
		}
		if p == drive || strings.HasPrefix(p, drive+"/") {
			return 2, nil
		}
		return 1, nil
	}
	tr.mountPoints = func() []string { return []string{"/", drive} }
	return tr, home, drive
}

func TestTrashOnOtherDrive(t *testing.T) {
	tr, home, drive := driveTrash(t)
	uid := strconv.Itoa(os.Getuid())
	own := filepath.Join(drive, ".Trash-"+uid)

	write(t, filepath.Join(home, "h.txt"), "h")
	write(t, filepath.Join(drive, "photos", "p.jpg"), "p")
	if name, err := tr.Put(filepath.Join(home, "h.txt")); err != nil || name != "h.txt" {
		t.Fatalf("home put = %q, %v", name, err)
	}
	name, err := tr.Put(filepath.Join(drive, "photos", "p.jpg"))
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(own, "files", "p.jpg"); name != want {
		t.Fatalf("drive item name = %q, want %q", name, want)
	}
	if st, err := os.Stat(own); err != nil || st.Mode().Perm() != 0o700 {
		t.Fatalf("drive trash: %v %v", st, err)
	}
	// The drive's info holds a path relative to the drive's top.
	if info := read(t, filepath.Join(own, "info", "p.jpg.trashinfo")); !strings.Contains(info, "\nPath=photos/p.jpg\n") {
		t.Fatalf("trashinfo = %q", info)
	}

	items, err := tr.List()
	if err != nil || len(items) != 2 {
		t.Fatalf("list = %+v, %v", items, err)
	}
	byName := map[string]TrashItem{}
	for _, it := range items {
		byName[it.Name] = it
	}
	if byName[name].OriginalPath != filepath.Join(drive, "photos", "p.jpg") || byName["h.txt"].OriginalPath != filepath.Join(home, "h.txt") {
		t.Fatalf("items = %+v", items)
	}
	if got := tr.FilesDirs(); len(got) != 2 || got[1] != filepath.Join(own, "files") {
		t.Fatalf("files dirs = %v", got)
	}
	if info, ok := tr.InfoFileFor(name); !ok || info != filepath.Join(own, "info", "p.jpg.trashinfo") {
		t.Fatalf("InfoFileFor = %q, %v", info, ok)
	}

	// Trash folders themselves cannot be trashed.
	for _, p := range []string{own, name, filepath.Join(tr.Dir(), "files", "h.txt")} {
		if _, err := tr.Put(p); err == nil {
			t.Fatalf("Put(%s) succeeded", p)
		}
	}

	if orig, err := tr.Restore(name); err != nil || orig != filepath.Join(drive, "photos", "p.jpg") || !exists(orig) {
		t.Fatalf("restore = %q, %v", orig, err)
	}
	if _, err := tr.Restore(filepath.Join(drive, "elsewhere", "files", "x")); err == nil {
		t.Fatal("restored from a folder that is not a trash")
	}

	if _, err := tr.Put(filepath.Join(drive, "photos", "p.jpg")); err != nil {
		t.Fatal(err)
	}
	if err := tr.Empty(); err != nil {
		t.Fatal(err)
	}
	if items, _ := tr.List(); len(items) != 0 {
		t.Fatalf("after empty: %+v", items)
	}
}

func TestTrashDriveAdminTrash(t *testing.T) {
	tr, _, drive := driveTrash(t)
	uid := strconv.Itoa(os.Getuid())
	admin := filepath.Join(drive, ".Trash")

	// Without the sticky bit the administrator's .Trash is not trusted.
	if err := os.Mkdir(admin, 0o777); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(drive, "a"), "a")
	name, err := tr.Put(filepath.Join(drive, "a"))
	if err != nil || !strings.Contains(name, "/.Trash-"+uid+"/") {
		t.Fatalf("non-sticky .Trash: %q, %v", name, err)
	}

	if err := os.Chmod(admin, 0o777|os.ModeSticky); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(drive, "b"), "b")
	name, err = tr.Put(filepath.Join(drive, "b"))
	if want := filepath.Join(admin, uid, "files", "b"); err != nil || name != want {
		t.Fatalf("sticky .Trash: %q, %v; want %s", name, err, want)
	}
	// Both trashes are listed.
	if items, _ := tr.List(); len(items) != 2 {
		t.Fatalf("items = %+v", items)
	}
}

func TestTrashDriveRefusesPlantedTrash(t *testing.T) {
	tr, _, drive := driveTrash(t)
	uid := strconv.Itoa(os.Getuid())
	elsewhere := t.TempDir()

	// A .Trash-$uid that is a symlink (planted by someone else) is never
	// followed; the item cannot be trashed and stays put.
	if err := os.Symlink(elsewhere, filepath.Join(drive, ".Trash-"+uid)); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(drive, "x"), "x")
	if _, err := tr.Put(filepath.Join(drive, "x")); !errors.Is(err, ErrOtherDrive) {
		t.Fatalf("put with planted symlink: %v", err)
	}
	if !exists(filepath.Join(drive, "x")) || exists(filepath.Join(elsewhere, "files")) {
		t.Fatal("item moved through the planted trash")
	}
	if _, ok := tr.InfoFileFor(filepath.Join(drive, ".Trash-"+uid, "files", "x")); ok {
		t.Fatal("planted trash recognized")
	}

	// Another device that is not a drive (tmpfs, say) has no trash.
	tr.mountPoints = func() []string { return []string{"/"} }
	write(t, filepath.Join(drive, "z"), "z")
	os.Remove(filepath.Join(drive, ".Trash-"+uid))
	if _, err := tr.Put(filepath.Join(drive, "z")); !errors.Is(err, ErrOtherDrive) {
		t.Fatalf("put on a non-drive mount: %v", err)
	}
	tr.mountPoints = func() []string { return []string{"/", drive} }

	// A drive the user cannot write to has no trash either.
	ro := filepath.Join(drive, "ro")
	write(t, filepath.Join(ro, "y"), "y")
	os.Remove(filepath.Join(drive, ".Trash-"+uid))
	if err := os.Chmod(drive, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(drive, 0o755) })
	if os.Getuid() != 0 {
		if _, err := tr.Put(filepath.Join(ro, "y")); !errors.Is(err, ErrOtherDrive) {
			t.Fatalf("put on read-only top: %v", err)
		}
	}
}

func TestTrashDriveInfoCannotEscape(t *testing.T) {
	tr, _, drive := driveTrash(t)
	own := filepath.Join(drive, ".Trash-"+strconv.Itoa(os.Getuid()))
	write(t, filepath.Join(own, "files", "evil"), "x")
	write(t, filepath.Join(own, "info", "evil.trashinfo"), "[Trash Info]\nPath=../../etc/evil\nDeletionDate=2026-01-01T00:00:00\n")
	if err := os.Chmod(own, 0o700); err != nil {
		t.Fatal(err)
	}
	if items, _ := tr.List(); len(items) != 0 {
		t.Fatalf("listed an item restoring outside the drive: %+v", items)
	}
	if _, err := tr.Restore(filepath.Join(own, "files", "evil")); err == nil {
		t.Fatal("restored outside the drive")
	}
}
