package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
)

// Drives and volumes through udisks2 on the system bus (D39), as the user:
// udisks and polkit decide what the user may do, the way the kernel decides
// file access.
//
// Tilde lists every volume (mounted or not) and can unmount the user's own
// mounts and eject drives. It does not mount: polkit lets only an active
// local session mount without an administrator's password, a web session
// is not one, and Tilde has no authentication agent yet (phase 5).

// Volume is a filesystem the user can see: mounted, or on a drive udisks
// knows about.
type Volume struct {
	// ID names the volume in requests: a udisks block object path, or
	// "mount:" and the mount point for mounts udisks does not know.
	ID         string `json:"id"`
	Device     string `json:"device"`
	Name       string `json:"name"`
	FSType     string `json:"fsType"`
	Size       uint64 `json:"size"`
	Free       uint64 `json:"free,omitempty"`
	MountPoint string `json:"mountPoint,omitempty"`
	// Removable is set for USB sticks, card readers and optical drives.
	Removable bool `json:"removable"`
	// CanEject offers Eject: a mounted volume under the user's /media, or
	// on a removable drive.
	CanEject bool `json:"canEject"`
	// System marks volumes udisks considers part of the system.
	System bool     `json:"system"`
	Icons  []string `json:"icons"`
	// drive is the udisks drive object, to eject all its volumes.
	drive dbus.ObjectPath
}

const (
	udisksName      = "org.freedesktop.UDisks2"
	udisksPath      = "/org/freedesktop/UDisks2"
	udisksBlock     = "org.freedesktop.UDisks2.Block"
	udisksFS        = "org.freedesktop.UDisks2.Filesystem"
	udisksDrive     = "org.freedesktop.UDisks2.Drive"
	udisksErrPrefix = "org.freedesktop.UDisks2.Error."
)

type managedObjects = map[dbus.ObjectPath]map[string]map[string]dbus.Variant

// udisksSource is what Tilde needs from udisks; tests replace it.
type udisksSource interface {
	objects(ctx context.Context) (managedObjects, error)
	call(ctx context.Context, path dbus.ObjectPath, method string, args ...any) error
}

// driveService lists volumes and watches udisks for changes.
type driveService struct {
	publish func()

	mu       sync.Mutex
	src      udisksSource
	watching bool
	failedAt time.Time
	closed   bool
	conn     *dbus.Conn
	timer    *time.Timer
}

func newDriveService(publish func()) *driveService {
	return &driveService{publish: publish}
}

// source connects to udisks on first use. Without a system bus or udisks
// (containers, minimal servers) the volume list falls back to the mount
// table; a failed connection is retried after a minute.
func (d *driveService) source() udisksSource {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.src != nil || d.closed || time.Since(d.failedAt) < time.Minute {
		return d.src
	}
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		slog.Info("worker: no system bus; drives come from the mount table only", "err", err)
		d.failedAt = time.Now()
		return nil
	}
	d.conn = conn
	d.src = &busUdisks{conn: conn}
	d.watch(conn)
	return d.src
}

// watch reports udisks changes (drives plugged in, volumes mounted) as one
// "drives" event per burst. Called with mu held.
func (d *driveService) watch(conn *dbus.Conn) {
	if d.watching {
		return
	}
	if err := conn.AddMatchSignal(dbus.WithMatchSender(udisksName), dbus.WithMatchPathNamespace(udisksPath)); err != nil {
		slog.Warn("worker: watch udisks", "err", err)
		return
	}
	d.watching = true
	ch := make(chan *dbus.Signal, 64)
	conn.Signal(ch)
	go func() {
		for range ch {
			d.mu.Lock()
			if d.timer == nil && !d.closed {
				d.timer = time.AfterFunc(300*time.Millisecond, func() {
					d.mu.Lock()
					d.timer = nil
					closed := d.closed
					d.mu.Unlock()
					if !closed {
						d.publish()
					}
				})
			}
			d.mu.Unlock()
		}
	}()
}

func (d *driveService) close() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.closed = true
	if d.timer != nil {
		d.timer.Stop()
	}
	if d.conn != nil {
		d.conn.Close()
	}
}

// busUdisks talks to the real udisks daemon.
type busUdisks struct{ conn *dbus.Conn }

func (b *busUdisks) objects(ctx context.Context) (managedObjects, error) {
	var objs managedObjects
	err := b.conn.Object(udisksName, udisksPath).
		CallWithContext(ctx, "org.freedesktop.DBus.ObjectManager.GetManagedObjects", 0).Store(&objs)
	return objs, err
}

func (b *busUdisks) call(ctx context.Context, path dbus.ObjectPath, method string, args ...any) error {
	return b.conn.Object(udisksName, path).CallWithContext(ctx, method, 0, args...).Err
}

// noInteraction keeps udisks from waiting for a polkit agent Tilde does
// not have: requests that need a password fail at once.
var noInteraction = map[string]dbus.Variant{"auth.no_user_interaction": dbus.MakeVariant(true)}

// volumes lists what the Computer view shows: udisks' volumes (when udisks
// is reachable) merged with the mount table, the root first, then mounted
// volumes, then unmounted ones, each by name.
func (d *driveService) volumes(ctx context.Context, user string) ([]Volume, bool) {
	var objs managedObjects
	src := d.source()
	if src != nil {
		var err error
		if objs, err = src.objects(ctx); err != nil {
			slog.Info("worker: list udisks objects", "err", err)
			src = nil
		}
	}
	return volumesFrom(objs, mounts(), user), src != nil
}

func volumesFrom(objs managedObjects, mounted []Mount, user string) []Volume {
	var out []Volume
	known := map[string]bool{}
	for path, ifaces := range objs {
		block, ok := ifaces[udisksBlock]
		fsys, isFS := ifaces[udisksFS]
		if !ok || !isFS || prop[bool](block, "HintIgnore") {
			continue
		}
		if t := prop[string](block, "IdType"); t == "squashfs" || t == "swap" {
			continue
		}
		v := Volume{
			ID:     string(path),
			Device: cString(prop[[]byte](block, "PreferredDevice")),
			FSType: prop[string](block, "IdType"),
			Size:   prop[uint64](block, "Size"),
			System: prop[bool](block, "HintSystem"),
		}
		if v.Device == "" {
			v.Device = cString(prop[[]byte](block, "Device"))
		}
		if mps := prop[[][]byte](fsys, "MountPoints"); len(mps) > 0 {
			v.MountPoint = cString(mps[0])
		}
		if strings.HasPrefix(v.MountPoint, "/snap/") {
			continue
		}
		drivePath := prop[dbus.ObjectPath](block, "Drive")
		drive := objs[drivePath][udisksDrive]
		if drive != nil {
			v.drive = drivePath
			bus := prop[string](drive, "ConnectionBus")
			v.Removable = prop[bool](drive, "Removable") || prop[bool](drive, "MediaRemovable") || bus == "usb" || bus == "sdio"
		}
		v.Name = firstNonEmpty(prop[string](block, "HintName"), prop[string](block, "IdLabel"))
		if v.Name == "" && v.MountPoint != "" && v.MountPoint != "/" {
			// No label: the folder it is mounted on says more than its size.
			v.Name = filepath.Base(v.MountPoint)
		}
		if v.Name == "" {
			v.Name = formatSizeSI(v.Size) + " Volume"
		}
		v.Icons = volumeIcons(drive, v.Removable)
		// What udisks mounted for the user (under /media) is theirs to eject,
		// whatever the drive; elsewhere only removable, non-system drives.
		v.CanEject = v.MountPoint != "" && (underMedia(v.MountPoint, user) || v.Removable && !v.System)
		known[v.Device] = true
		if v.MountPoint != "" {
			known["mount:"+v.MountPoint] = true
		}
		out = append(out, v)
	}
	for _, m := range mounted {
		if known[m.Device] || known["mount:"+m.MountPoint] {
			// udisks reports sizes, not free space.
			for i := range out {
				if out[i].MountPoint == m.MountPoint {
					out[i].Free = m.Free
				}
			}
			continue
		}
		out = append(out, Volume{
			ID: "mount:" + m.MountPoint, Device: m.Device, FSType: m.FSType, Size: m.Size, Free: m.Free,
			MountPoint: m.MountPoint, Name: filepath.Base(m.MountPoint), Icons: []string{"drive-harddisk"},
		})
	}
	rank := func(v Volume) int {
		switch {
		case v.MountPoint == "/":
			return 0
		case v.MountPoint != "":
			return 1
		}
		return 2
	}
	sort.SliceStable(out, func(i, j int) bool {
		if ri, rj := rank(out[i]), rank(out[j]); ri != rj {
			return ri < rj
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}

// underMedia: where udisks mounts the user's drives.
func underMedia(mp, user string) bool {
	return strings.HasPrefix(mp, "/media/"+user+"/") || strings.HasPrefix(mp, "/run/media/"+user+"/")
}

func volumeIcons(drive map[string]dbus.Variant, removable bool) []string {
	switch {
	case drive != nil && prop[bool](drive, "Optical"),
		drive != nil && slices.ContainsFunc(prop[[]string](drive, "MediaCompatibility"), func(s string) bool {
			return strings.HasPrefix(s, "optical")
		}):
		return []string{"media-optical", "drive-optical"}
	case drive != nil && slices.ContainsFunc(prop[[]string](drive, "MediaCompatibility"), func(s string) bool {
		return strings.HasPrefix(s, "flash")
	}):
		return []string{"media-flash", "drive-removable-media-usb", "drive-harddisk"}
	case removable:
		return []string{"drive-removable-media-usb", "drive-harddisk"}
	}
	return []string{"drive-harddisk"}
}

// prop reads a udisks property of type T, or its zero value.
func prop[T any](props map[string]dbus.Variant, name string) T {
	var zero T
	v, ok := props[name]
	if !ok {
		return zero
	}
	t, ok := v.Value().(T)
	if !ok {
		return zero
	}
	return t
}

// cString turns udisks' NUL-terminated byte strings into Go strings.
func cString(b []byte) string {
	if i := slices.Index(b, 0); i >= 0 {
		b = b[:i]
	}
	return string(b)
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}

// formatSizeSI formats bytes the way GLib (and so Nemo) names volumes:
// "4.0 GB", in powers of 1000.
func formatSizeSI(n uint64) string {
	units := []string{"bytes", "kB", "MB", "GB", "TB", "PB"}
	f := float64(n)
	i := 0
	for f >= 1000 && i < len(units)-1 {
		f /= 1000
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%d bytes", n)
	}
	return fmt.Sprintf("%.1f %s", f, units[i])
}

// errNotAuthorized: polkit wants a password Tilde cannot ask for yet.
var errNotAuthorized = errors.New("not authorized")

// udisksErr turns udisks errors into API errors.
func udisksErr(err error) (int, string) {
	var de dbus.Error
	if errors.As(err, &de) {
		switch strings.TrimPrefix(de.Name, udisksErrPrefix) {
		case "NotAuthorized", "NotAuthorizedCanObtain", "NotAuthorizedDismissed":
			return http.StatusForbidden, "not_authorized"
		case "DeviceBusy":
			return http.StatusConflict, "busy"
		}
	}
	if errors.Is(err, errNotAuthorized) {
		return http.StatusForbidden, "not_authorized"
	}
	return http.StatusBadGateway, "udisks_error"
}

func udisksErrIs(err error, names ...string) bool {
	var de dbus.Error
	return errors.As(err, &de) && slices.Contains(names, strings.TrimPrefix(de.Name, udisksErrPrefix))
}

func writeUdisksErr(rw http.ResponseWriter, err error) {
	status, code := udisksErr(err)
	msg := err.Error()
	switch code {
	case "busy":
		msg = "the volume is in use; close the files, folders and terminals that use it"
	case "not_authorized":
		msg = "this needs an administrator's password, which Tilde cannot ask for yet"
	}
	writeJSON(rw, status, apiError{Error: msg, Code: code})
}

func (w *Worker) handleDrives(rw http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	vols, udisks := w.drives.volumes(ctx, w.info.Name)
	if vols == nil {
		vols = []Volume{}
	}
	writeJSON(rw, http.StatusOK, map[string]any{"udisks": udisks, "volumes": vols})
}

// findVolume looks id up among the current volumes, so requests can only
// name volumes udisks lists.
func (w *Worker) findVolume(ctx context.Context, id string) ([]Volume, *Volume, error) {
	vols, _ := w.drives.volumes(ctx, w.info.Name)
	for i := range vols {
		if vols[i].ID == id && strings.HasPrefix(id, udisksPath+"/") {
			return vols, &vols[i], nil
		}
	}
	return nil, nil, badRequest{"no such volume (only volumes udisks manages can be unmounted)"}
}

func (w *Worker) unmount(ctx context.Context, v *Volume) error {
	err := w.drives.source().call(ctx, dbus.ObjectPath(v.ID), udisksFS+".Unmount", noInteraction)
	if udisksErrIs(err, "NotMounted") {
		return nil
	}
	return err
}

// handleUnmount unmounts one volume. udisks lets users unmount what they
// mounted without asking polkit; unmounting someone else's mount needs an
// administrator.
func (w *Worker) handleUnmount(rw http.ResponseWriter, r *http.Request) {
	var req struct {
		ID string `json:"id"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(rw, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	_, v, err := w.findVolume(ctx, req.ID)
	if err != nil {
		writeErr(rw, err)
		return
	}
	if err := w.unmount(ctx, v); err != nil {
		writeUdisksErr(rw, err)
		return
	}
	rw.WriteHeader(http.StatusNoContent)
}

// handleEject unmounts every mounted volume of the drive, then powers the
// drive off (USB) or ejects the medium. Without an active local session
// polkit refuses the last step; the drive is unmounted and safe to unplug
// anyway, which the response says.
func (w *Worker) handleEject(rw http.ResponseWriter, r *http.Request) {
	var req struct {
		ID string `json:"id"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(rw, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	vols, v, err := w.findVolume(ctx, req.ID)
	if err != nil {
		writeErr(rw, err)
		return
	}
	for i := range vols {
		o := &vols[i]
		same := o.ID == v.ID || (v.drive != "" && o.drive == v.drive)
		if same && o.MountPoint != "" {
			if err := w.unmount(ctx, o); err != nil {
				writeUdisksErr(rw, err)
				return
			}
		}
	}
	res := map[string]any{"unmounted": true, "ejected": false}
	if v.drive != "" {
		objs, err := w.drives.source().objects(ctx)
		if err == nil {
			method := udisksDrive + ".Eject"
			if prop[bool](objs[v.drive][udisksDrive], "CanPowerOff") {
				method = udisksDrive + ".PowerOff"
			}
			err = w.drives.source().call(ctx, v.drive, method, noInteraction)
		}
		if err == nil {
			res["ejected"] = true
		} else if status, _ := udisksErr(err); status != http.StatusForbidden {
			slog.Info("worker: eject", "drive", v.drive, "err", err)
		}
	}
	writeJSON(rw, http.StatusOK, res)
}
