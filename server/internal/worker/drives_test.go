package worker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"

	"tilde/internal/userinfo"
)

// fakeUdisks stands in for the udisks daemon.
type fakeUdisks struct {
	objs  managedObjects
	fail  map[string]error // by "path method"
	mu    sync.Mutex
	calls []string
}

func (f *fakeUdisks) objects(context.Context) (managedObjects, error) { return f.objs, nil }

func (f *fakeUdisks) call(_ context.Context, path dbus.ObjectPath, method string, args ...any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := string(path) + " " + method[strings.LastIndex(method, ".")+1:]
	f.calls = append(f.calls, key)
	if opts, ok := args[0].(map[string]dbus.Variant); !ok || opts["auth.no_user_interaction"].Value() != true {
		return dbus.Error{Name: "test.MissingNoInteraction"}
	}
	return f.fail[key]
}

func v(x any) dbus.Variant { return dbus.MakeVariant(x) }

func cbytes(s string) []byte { return append([]byte(s), 0) }

func block(dev, label, fstype string, size uint64, drive string, system bool, mps ...string) map[string]map[string]dbus.Variant {
	var points [][]byte
	for _, mp := range mps {
		points = append(points, cbytes(mp))
	}
	return map[string]map[string]dbus.Variant{
		udisksBlock: {
			"Device": v(cbytes(dev)), "PreferredDevice": v(cbytes(dev)), "IdLabel": v(label),
			"IdType": v(fstype), "Size": v(size), "Drive": v(dbus.ObjectPath(drive)), "HintSystem": v(system),
			"HintIgnore": v(false), "HintName": v(""),
		},
		udisksFS: {"MountPoints": v(points)},
	}
}

const (
	pathRoot  = udisksPath + "/block_devices/sda2"
	pathStick = udisksPath + "/block_devices/sdb1"
	pathStk2  = udisksPath + "/block_devices/sdb2"
	pathSpare = udisksPath + "/block_devices/sda3"
	driveSSD  = udisksPath + "/drives/SSD"
	driveUSB  = udisksPath + "/drives/Stick"
)

func testObjects() managedObjects {
	swap := block("/dev/sda4", "", "swap", 1e9, driveSSD, true)
	ignored := block("/dev/sda1", "EFI", "vfat", 5e8, driveSSD, true, "/boot/efi")
	ignored[udisksBlock]["HintIgnore"] = v(true)
	return managedObjects{
		pathRoot:                           block("/dev/sda2", "", "ext4", 5e11, driveSSD, true, "/"),
		pathSpare:                          block("/dev/sda3", "", "ext4", 4e9, driveSSD, true),
		pathStick:                          block("/dev/sdb1", "STICK", "vfat", 16e9, driveUSB, false, "/media/me/STICK"),
		pathStk2:                           block("/dev/sdb2", "", "ext4", 1e9, driveUSB, false, "/media/me/Data"),
		udisksPath + "/block_devices/sda4": swap,
		udisksPath + "/block_devices/sda1": ignored,
		driveSSD:                           {udisksDrive: {"ConnectionBus": v(""), "Removable": v(false), "CanPowerOff": v(false)}},
		driveUSB: {udisksDrive: {
			"ConnectionBus": v("usb"), "Removable": v(true), "CanPowerOff": v(true),
			"MediaCompatibility": v([]string{"thumb"}),
		}},
	}
}

func TestVolumesFrom(t *testing.T) {
	mounted := []Mount{
		{Device: "/dev/sda2", MountPoint: "/", FSType: "ext4", Size: 5e11, Free: 1e11},
		{Device: "/dev/sdc1", MountPoint: "/mnt/backup", FSType: "xfs", Size: 2e12, Free: 1e12},
	}
	got := volumesFrom(testObjects(), mounted, "me")
	type row struct {
		Name, MountPoint     string
		Removable, CanEject  bool
		Free                 uint64
		FirstIcon, Partition string
	}
	var rows []row
	for _, vol := range got {
		rows = append(rows, row{vol.Name, vol.MountPoint, vol.Removable, vol.CanEject, vol.Free, vol.Icons[0], vol.Device})
	}
	want := []row{
		{"500.0 GB Volume", "/", false, false, 1e11, "drive-harddisk", "/dev/sda2"},
		{"backup", "/mnt/backup", false, false, 1e12, "drive-harddisk", "/dev/sdc1"},
		{"Data", "/media/me/Data", true, true, 0, "drive-removable-media-usb", "/dev/sdb2"},
		{"STICK", "/media/me/STICK", true, true, 0, "drive-removable-media-usb", "/dev/sdb1"},
		{"4.0 GB Volume", "", false, false, 0, "drive-harddisk", "/dev/sda3"},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("volumes:\n got %+v\nwant %+v", rows, want)
	}
}

func driveWorker(t *testing.T, f *fakeUdisks) *Worker {
	t.Helper()
	info, err := userinfo.Current()
	if err != nil {
		t.Fatal(err)
	}
	info.Name = "me"
	w := New(info, Options{DataHome: t.TempDir(), ConfigHome: t.TempDir(), CacheHome: t.TempDir(), ThumbnailerDirs: []string{}})
	if f != nil {
		w.drives.src = f
	} else {
		w.drives.failedAt = time.Now() // no udisks: do not try the real bus
	}
	t.Cleanup(w.Shutdown)
	return w
}

func serve(t *testing.T, w *Worker, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	rec := httptest.NewRecorder()
	w.Handler().ServeHTTP(rec, req)
	return rec
}

func TestEjectUnmountsTheDriveThenPowersOff(t *testing.T) {
	f := &fakeUdisks{objs: testObjects(), fail: map[string]error{
		// A web session is not an active local one: polkit refuses.
		driveUSB + " PowerOff": dbus.Error{Name: udisksErrPrefix + "NotAuthorizedCanObtain"},
		pathStk2 + " Unmount":  dbus.Error{Name: udisksErrPrefix + "NotMounted"},
	}}
	w := driveWorker(t, f)
	rec := serve(t, w, "POST", "/api/v1/drives/eject", `{"id":"`+pathStick+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("eject: %d %s", rec.Code, rec.Body)
	}
	var res map[string]bool
	json.Unmarshal(rec.Body.Bytes(), &res)
	if !res["unmounted"] || res["ejected"] {
		t.Fatalf("eject result = %v", res)
	}
	want := []string{pathStk2 + " Unmount", pathStick + " Unmount", driveUSB + " PowerOff"}
	if !reflect.DeepEqual(f.calls, want) {
		t.Fatalf("calls = %v, want %v", f.calls, want)
	}

	delete(f.fail, driveUSB+" PowerOff")
	rec = serve(t, w, "POST", "/api/v1/drives/eject", `{"id":"`+pathStick+`"}`)
	json.Unmarshal(rec.Body.Bytes(), &res)
	if !res["ejected"] {
		t.Fatalf("eject with permission = %v", res)
	}
}

func TestUnmountErrors(t *testing.T) {
	f := &fakeUdisks{objs: testObjects(), fail: map[string]error{
		pathStick + " Unmount": dbus.Error{Name: udisksErrPrefix + "DeviceBusy"},
		pathRoot + " Unmount":  dbus.Error{Name: udisksErrPrefix + "NotAuthorized"},
	}}
	w := driveWorker(t, f)
	for _, c := range []struct {
		id   string
		code int
		api  string
	}{
		{pathStick, http.StatusConflict, "busy"},
		{pathRoot, http.StatusForbidden, "not_authorized"},
		{pathStk2, http.StatusNoContent, ""},
		{udisksPath + "/block_devices/nope", http.StatusBadRequest, "bad_request"},
		{"mount:/mnt/backup", http.StatusBadRequest, "bad_request"},
		{"/org/freedesktop/UDisks2/Manager", http.StatusBadRequest, "bad_request"},
	} {
		rec := serve(t, w, "POST", "/api/v1/drives/unmount", `{"id":"`+c.id+`"}`)
		if rec.Code != c.code || !strings.Contains(rec.Body.String(), c.api) {
			t.Errorf("unmount %s: %d %s, want %d %s", c.id, rec.Code, rec.Body, c.code, c.api)
		}
	}
}

func TestDrivesWithoutUdisks(t *testing.T) {
	w := driveWorker(t, nil)
	rec := serve(t, w, "GET", "/api/v1/drives", "")
	var res struct {
		Udisks  bool
		Volumes []Volume
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("drives: %d %s", rec.Code, rec.Body)
	}
	// The mount table still lists the root filesystem's drive.
	if res.Udisks || len(res.Volumes) == 0 || res.Volumes[0].MountPoint == "" {
		t.Fatalf("drives without udisks = %+v", res)
	}
	if rec := serve(t, w, "POST", "/api/v1/drives/eject", `{"id":"`+pathStick+`"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("eject without udisks: %d", rec.Code)
	}
}

func TestFormatSizeSI(t *testing.T) {
	for n, want := range map[uint64]string{999: "999 bytes", 1000: "1.0 kB", 4e9: "4.0 GB", 15_931_539_456: "15.9 GB"} {
		if got := formatSizeSI(n); got != want {
			t.Errorf("formatSizeSI(%d) = %q, want %q", n, got, want)
		}
	}
}
