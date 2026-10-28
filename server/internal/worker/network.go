package worker

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"

	"tilde/internal/protocol"
)

// The network applet's view of NetworkManager on the system bus (D55), as
// the user: NetworkManager and polkit decide what the user may change, as
// udisks does for drives (D39). Only wired and Wi-Fi devices are shown;
// connecting asks polkit, which in a web session usually wants a password
// through Tilde's agent (D44).

const (
	nmName         = "org.freedesktop.NetworkManager"
	nmPath         = "/org/freedesktop/NetworkManager"
	nmObjects      = "/org/freedesktop"
	nmDevice       = nmName + ".Device"
	nmWireless     = nmName + ".Device.Wireless"
	nmAccessPoint  = nmName + ".AccessPoint"
	nmActive       = nmName + ".Connection.Active"
	nmIP4          = nmName + ".IP4Config"
	nmIP6          = nmName + ".IP6Config"
	nmSettingsConn = nmName + ".Settings.Connection"
	nmErrPrefix    = nmName + "."
)

// NetworkManager's device types and states (NMDeviceType, NMDeviceState).
const (
	nmTypeEthernet = 1
	nmTypeWifi     = 2

	nmStateUnmanaged    = 10
	nmStateUnavailable  = 20
	nmStateDisconnected = 30
	nmStateActivated    = 100
	nmStateDeactivating = 110
	nmStateFailed       = 120

	nmActiveActivated   = 2
	nmActiveDeactivated = 4
)

// Access point security flags (NM80211ApFlags, NM80211ApSecurityFlags).
const (
	apPrivacy  = 0x1
	apKeyPSK   = 0x100
	apKey8021X = 0x200
	apKeySAE   = 0x400
	apKeyOWE   = 0x800
)

// NetworkState is what the panel's network applet shows.
type NetworkState struct {
	// Available is false without NetworkManager (servers on systemd-networkd).
	Available bool `json:"available"`
	// WifiEnabled is the Wi-Fi switch; WifiHardware is the radio's own
	// (a laptop's airplane-mode key), which software cannot turn back on.
	WifiEnabled  bool        `json:"wifiEnabled"`
	WifiHardware bool        `json:"wifiHardware"`
	Devices      []NetDevice `json:"devices"`
}

// NetDevice is one wired or Wi-Fi device.
type NetDevice struct {
	ID        string `json:"id"`
	Interface string `json:"interface"`
	Type      string `json:"type"` // "ethernet" or "wifi"
	// State: connected, connecting, disconnected, unavailable (no cable,
	// Wi-Fi off), deactivating or failed.
	State      string `json:"state"`
	Connection string `json:"connection,omitempty"`
	// Addresses are the device's IP addresses, without prefix lengths.
	Addresses []string `json:"addresses,omitempty"`
	// Default: the device carries the default route, so most likely the
	// browser's connection to Tilde.
	Default  bool          `json:"default"`
	Networks []WifiNetwork `json:"networks,omitempty"`
}

// WifiNetwork is one network name a Wi-Fi device sees, with its strongest
// access point.
type WifiNetwork struct {
	SSID     string `json:"ssid"`
	Strength int    `json:"strength"`
	// Security: none, psk (WPA/WPA2 Personal), sae (WPA3 Personal only),
	// or enterprise and other, which need a connections editor.
	Security string `json:"security"`
	Active   bool   `json:"active"`
	// Known: NetworkManager has a saved connection for it.
	Known bool `json:"known"`

	ap dbus.ObjectPath
}

type connSettings = map[string]map[string]dbus.Variant

// nmSource is what Tilde needs from NetworkManager; tests replace it.
type nmSource interface {
	objects(ctx context.Context) (managedObjects, error)
	settings(ctx context.Context, conn dbus.ObjectPath) (connSettings, error)
	// call runs a method; interactive lets polkit ask for a password.
	call(ctx context.Context, path dbus.ObjectPath, method string, interactive bool, args ...any) ([]any, error)
	get(ctx context.Context, path dbus.ObjectPath, iface, name string) (dbus.Variant, error)
}

// networkService reads NetworkManager and watches it for changes.
type networkService struct {
	publish func()

	mu       sync.Mutex
	src      nmSource
	failedAt time.Time
	closed   bool
	conn     *dbus.Conn
	timer    *time.Timer
	// waiters get the reasons devices fail with, by device path.
	waiters map[dbus.ObjectPath][]chan uint32
}

func newNetworkService(publish func()) *networkService {
	return &networkService{publish: publish}
}

// source connects on first use; without NetworkManager the applet stays
// hidden. A failed connection is retried after a minute.
func (n *networkService) source() nmSource {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.src != nil || n.closed || time.Since(n.failedAt) < time.Minute {
		return n.src
	}
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		slog.Info("worker: no system bus for NetworkManager", "err", err)
		n.failedAt = time.Now()
		return nil
	}
	// Signals first, so no change between the first listing and the watch is missed.
	if err := conn.AddMatchSignal(dbus.WithMatchSender(nmName), dbus.WithMatchPathNamespace(nmPath)); err != nil {
		slog.Warn("worker: watch NetworkManager", "err", err)
	}
	ch := make(chan *dbus.Signal, 64)
	conn.Signal(ch)
	go n.forward(ch)
	n.conn = conn
	n.src = &busNM{conn: conn}
	return n.src
}

// forward turns NetworkManager's signals into one "network" event per
// burst. Access points report their strength every few seconds while
// scanning, so bursts are a second long.
func (n *networkService) forward(ch <-chan *dbus.Signal) {
	for sig := range ch {
		if sig.Name == nmDevice+".StateChanged" && len(sig.Body) == 3 {
			if state, _ := sig.Body[0].(uint32); state == nmStateFailed {
				reason, _ := sig.Body[2].(uint32)
				n.deviceFailed(sig.Path, reason)
			}
		}
		n.mu.Lock()
		if n.timer == nil && !n.closed {
			n.timer = time.AfterFunc(time.Second, func() {
				n.mu.Lock()
				n.timer = nil
				closed := n.closed
				n.mu.Unlock()
				if !closed {
					n.publish()
				}
			})
		}
		n.mu.Unlock()
	}
}

// watchFailures reports why dev fails, while a request waits on it. The
// device passes through "failed" to "disconnected" at once (and may start
// another connection), so its StateReason property no longer says why by
// the time anyone reads it: only the signal does.
func (n *networkService) watchFailures(dev dbus.ObjectPath) (<-chan uint32, func()) {
	ch := make(chan uint32, 4)
	n.mu.Lock()
	if n.waiters == nil {
		n.waiters = map[dbus.ObjectPath][]chan uint32{}
	}
	n.waiters[dev] = append(n.waiters[dev], ch)
	n.mu.Unlock()
	return ch, func() {
		n.mu.Lock()
		defer n.mu.Unlock()
		n.waiters[dev] = slices.DeleteFunc(n.waiters[dev], func(c chan uint32) bool { return c == ch })
	}
}

func (n *networkService) deviceFailed(dev dbus.ObjectPath, reason uint32) {
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, ch := range n.waiters[dev] {
		select {
		case ch <- reason:
		default:
		}
	}
}

func (n *networkService) close() {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.closed = true
	if n.timer != nil {
		n.timer.Stop()
	}
	if n.conn != nil {
		n.conn.Close()
	}
}

// busNM talks to the real NetworkManager.
type busNM struct{ conn *dbus.Conn }

func (b *busNM) objects(ctx context.Context) (managedObjects, error) {
	var objs managedObjects
	err := b.conn.Object(nmName, nmObjects).
		CallWithContext(ctx, "org.freedesktop.DBus.ObjectManager.GetManagedObjects", 0).Store(&objs)
	return objs, err
}

func (b *busNM) settings(ctx context.Context, conn dbus.ObjectPath) (connSettings, error) {
	var s connSettings
	err := b.conn.Object(nmName, conn).CallWithContext(ctx, nmSettingsConn+".GetSettings", 0).Store(&s)
	return s, err
}

func (b *busNM) call(ctx context.Context, path dbus.ObjectPath, method string, interactive bool, args ...any) ([]any, error) {
	c := callWith(ctx, b.conn, nmName, path, method, interactive, args...)
	return c.Body, c.Err
}

func (b *busNM) get(ctx context.Context, path dbus.ObjectPath, iface, name string) (dbus.Variant, error) {
	var v dbus.Variant
	err := b.conn.Object(nmName, path).CallWithContext(ctx, "org.freedesktop.DBus.Properties.Get", 0, iface, name).Store(&v)
	return v, err
}

// state reads the whole picture: one listing of NetworkManager's objects,
// plus the saved Wi-Fi connections' names, which are not properties.
func (n *networkService) state(ctx context.Context) NetworkState {
	src := n.source()
	if src == nil {
		return NetworkState{Devices: []NetDevice{}}
	}
	objs, err := src.objects(ctx)
	if err != nil || objs[nmPath][nmName] == nil {
		slog.Debug("worker: list NetworkManager objects", "err", err)
		return NetworkState{Devices: []NetDevice{}}
	}
	return networkFrom(objs, n.knownSSIDs(ctx, src, objs))
}

// knownSSIDs maps the network names of saved Wi-Fi connections to them.
// Connections the user may not see fail to load and are left out.
func (n *networkService) knownSSIDs(ctx context.Context, src nmSource, objs managedObjects) map[string]dbus.ObjectPath {
	known := map[string]dbus.ObjectPath{}
	paths := make([]dbus.ObjectPath, 0)
	for path, ifaces := range objs {
		if _, ok := ifaces[nmSettingsConn]; ok {
			paths = append(paths, path)
		}
	}
	// The same name saved twice: the first by path, as stable as any.
	slices.Sort(paths)
	for _, path := range paths {
		s, err := src.settings(ctx, path)
		if err != nil {
			continue
		}
		if ssid := prop[[]byte](s["802-11-wireless"], "ssid"); len(ssid) > 0 {
			if _, dup := known[string(ssid)]; !dup {
				known[string(ssid)] = path
			}
		}
	}
	return known
}

func networkFrom(objs managedObjects, known map[string]dbus.ObjectPath) NetworkState {
	nm := objs[nmPath][nmName]
	st := NetworkState{
		Available:    true,
		WifiEnabled:  prop[bool](nm, "WirelessEnabled"),
		WifiHardware: prop[bool](nm, "WirelessHardwareEnabled"),
		Devices:      []NetDevice{},
	}
	for path, ifaces := range objs {
		dev := ifaces[nmDevice]
		if dev == nil {
			continue
		}
		kind := ""
		switch prop[uint32](dev, "DeviceType") {
		case nmTypeEthernet:
			kind = "ethernet"
		case nmTypeWifi:
			kind = "wifi"
		default:
			continue
		}
		state := prop[uint32](dev, "State")
		if state <= nmStateUnmanaged {
			continue
		}
		d := NetDevice{
			ID:        string(path),
			Interface: prop[string](dev, "Interface"),
			Type:      kind,
			State:     deviceState(state),
		}
		if ac := objs[prop[dbus.ObjectPath](dev, "ActiveConnection")][nmActive]; ac != nil {
			d.Connection = prop[string](ac, "Id")
			d.Default = prop[bool](ac, "Default") || prop[bool](ac, "Default6")
		}
		d.Addresses = append(addresses(objs[prop[dbus.ObjectPath](dev, "Ip4Config")][nmIP4]),
			addresses(objs[prop[dbus.ObjectPath](dev, "Ip6Config")][nmIP6])...)
		if kind == "wifi" {
			d.Networks = wifiNetworks(objs, ifaces[nmWireless], known)
		}
		st.Devices = append(st.Devices, d)
	}
	sort.Slice(st.Devices, func(i, j int) bool {
		a, b := st.Devices[i], st.Devices[j]
		if a.Type != b.Type {
			return a.Type == "ethernet"
		}
		return a.Interface < b.Interface
	})
	return st
}

func deviceState(s uint32) string {
	switch {
	case s == nmStateActivated:
		return "connected"
	case s == nmStateUnavailable:
		return "unavailable"
	case s == nmStateDisconnected:
		return "disconnected"
	case s == nmStateDeactivating:
		return "deactivating"
	case s == nmStateFailed:
		return "failed"
	case s > nmStateDisconnected && s < nmStateActivated:
		return "connecting"
	}
	return "unavailable"
}

// addresses lists an IP config's addresses, link-local IPv6 ones left out.
func addresses(cfg map[string]dbus.Variant) []string {
	var out []string
	for _, a := range prop[[]map[string]dbus.Variant](cfg, "AddressData") {
		addr := prop[string](a, "address")
		if ip := net.ParseIP(addr); ip == nil || ip.IsLinkLocalUnicast() {
			continue
		}
		out = append(out, addr)
	}
	return out
}

// wifiNetworks groups a device's access points by network name, keeping
// the strongest of each; hidden networks (no name) are left out. The
// connected network comes first, then by strength.
func wifiNetworks(objs managedObjects, wireless map[string]dbus.Variant, known map[string]dbus.ObjectPath) []WifiNetwork {
	activeAP := prop[dbus.ObjectPath](wireless, "ActiveAccessPoint")
	activeSSID := ""
	if ap := objs[activeAP][nmAccessPoint]; ap != nil {
		activeSSID = string(prop[[]byte](ap, "Ssid"))
	}
	byName := map[string]*WifiNetwork{}
	for _, path := range prop[[]dbus.ObjectPath](wireless, "AccessPoints") {
		ap := objs[path][nmAccessPoint]
		ssid := string(prop[[]byte](ap, "Ssid"))
		if ap == nil || ssid == "" {
			continue
		}
		strength := int(prop[byte](ap, "Strength"))
		if cur := byName[ssid]; cur != nil && cur.Strength >= strength {
			continue
		}
		_, isKnown := known[ssid]
		byName[ssid] = &WifiNetwork{
			SSID: ssid, Strength: strength, Security: apSecurity(ap),
			Active: ssid == activeSSID, Known: isKnown, ap: path,
		}
	}
	out := make([]WifiNetwork, 0, len(byName))
	for _, w := range byName {
		out = append(out, *w)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Active != b.Active {
			return a.Active
		}
		if a.Strength != b.Strength {
			return a.Strength > b.Strength
		}
		return a.SSID < b.SSID
	})
	return out
}

func apSecurity(ap map[string]dbus.Variant) string {
	wpa := prop[uint32](ap, "WpaFlags")
	rsn := prop[uint32](ap, "RsnFlags")
	switch {
	case (wpa|rsn)&apKey8021X != 0:
		return "enterprise"
	case (wpa|rsn)&apKeyPSK != 0:
		// WPA2 Personal, and WPA2/WPA3 transition networks.
		return "psk"
	case rsn&apKeySAE != 0:
		return "sae"
	case rsn&apKeyOWE != 0, prop[uint32](ap, "Flags")&apPrivacy != 0:
		// Enhanced Open and WEP.
		return "other"
	}
	return "none"
}

// writeNMErr turns NetworkManager errors into API errors.
func writeNMErr(rw http.ResponseWriter, err error) {
	var de dbus.Error
	if errors.As(err, &de) && strings.TrimPrefix(de.Name, nmErrPrefix) == "PermissionDenied" {
		writeJSON(rw, http.StatusForbidden, apiError{Error: "not allowed: this needs an administrator's password", Code: "not_authorized"})
		return
	}
	writeJSON(rw, http.StatusBadGateway, apiError{Error: err.Error(), Code: "network_error"})
}

func (w *Worker) handleNetwork(rw http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	writeJSON(rw, http.StatusOK, w.network.state(ctx))
}

// findNetDevice looks id up among the listed devices, so requests can only
// name wired and Wi-Fi devices NetworkManager manages.
func (w *Worker) findNetDevice(ctx context.Context, id string) (nmSource, *NetDevice, error) {
	src := w.network.source()
	if src == nil {
		return nil, nil, badRequest{"NetworkManager is not running"}
	}
	st := w.network.state(ctx)
	for i := range st.Devices {
		if st.Devices[i].ID == id {
			return src, &st.Devices[i], nil
		}
	}
	return nil, nil, badRequest{"no such network device"}
}

// interactive: whether polkit may ask for a password, which it can only
// through Tilde's agent.
func (w *Worker) interactive() bool { return w.polkit.running() }

// handleWifiSwitch turns Wi-Fi on or off.
func (w *Worker) handleWifiSwitch(rw http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(rw, err)
		return
	}
	src := w.network.source()
	if src == nil {
		writeErr(rw, badRequest{"NetworkManager is not running"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), authTimeout+time.Minute)
	defer cancel()
	_, err := src.call(ctx, nmPath, "org.freedesktop.DBus.Properties.Set", w.interactive(),
		nmName, "WirelessEnabled", dbus.MakeVariant(req.Enabled))
	if err != nil {
		writeNMErr(rw, err)
		return
	}
	ev := "wifi_off"
	if req.Enabled {
		ev = "wifi_on"
	}
	w.audit(r, protocol.AuditEvent{Event: ev})
	rw.WriteHeader(http.StatusNoContent)
}

// handleNetworkScan asks a Wi-Fi device to look for networks now, which the
// applet does when it opens. NetworkManager scans by itself every few
// minutes, so a scan polkit refuses (or one already running) is no error.
func (w *Worker) handleNetworkScan(rw http.ResponseWriter, r *http.Request) {
	var req struct {
		Device string `json:"device"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(rw, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	src, d, err := w.findNetDevice(ctx, req.Device)
	if err != nil {
		writeErr(rw, err)
		return
	}
	if d.Type == "wifi" {
		if _, err := src.call(ctx, dbus.ObjectPath(d.ID), nmWireless+".RequestScan", false, map[string]dbus.Variant{}); err != nil {
			slog.Debug("worker: wifi scan", "err", err)
		}
	}
	rw.WriteHeader(http.StatusNoContent)
}

// connectTimeout bounds the wait for a connection to come up; DHCP on a
// slow network is the usual long step.
var connectTimeout = 45 * time.Second

// handleNetworkConnect brings a device up: a wired device with whatever
// connection NetworkManager picks, a Wi-Fi device on the named network,
// with its saved connection or a new one. It waits until the connection is
// up or has failed, and says which.
func (w *Worker) handleNetworkConnect(rw http.ResponseWriter, r *http.Request) {
	var req struct {
		Device   string `json:"device"`
		SSID     string `json:"ssid"`
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(rw, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), authTimeout+time.Minute+connectTimeout)
	defer cancel()
	src, d, err := w.findNetDevice(ctx, req.Device)
	if err != nil {
		writeErr(rw, err)
		return
	}
	dev := dbus.ObjectPath(d.ID)
	failures, stop := w.network.watchFailures(dev)
	defer stop()
	var conn, ap dbus.ObjectPath = "/", "/"
	added := false
	var active dbus.ObjectPath
	if d.Type == "wifi" {
		i := slices.IndexFunc(d.Networks, func(n WifiNetwork) bool { return n.SSID == req.SSID })
		if i < 0 {
			writeErr(rw, badRequest{"no such network in range"})
			return
		}
		wn := d.Networks[i]
		ap = wn.ap
		if wn.Known {
			objs, err := src.objects(ctx)
			if err != nil {
				writeNMErr(rw, err)
				return
			}
			if saved, ok := w.network.knownSSIDs(ctx, src, objs)[wn.SSID]; ok {
				conn = saved
			}
			if req.Password != "" && conn != "/" {
				// A new password for a saved network (the old one was refused).
				if err := setPassword(ctx, src, conn, req.Password, w.interactive()); err != nil {
					writeNMErr(rw, err)
					return
				}
			}
		} else {
			settings, err := newWifiSettings(wn, req.Password)
			if err != nil {
				writeErr(rw, err)
				return
			}
			body, err := src.call(ctx, nmPath, nmName+".AddAndActivateConnection", w.interactive(), settings, dev, ap)
			if err != nil {
				writeNMErr(rw, err)
				return
			}
			if len(body) == 2 {
				conn, _ = body[0].(dbus.ObjectPath)
				active, _ = body[1].(dbus.ObjectPath)
			}
			added = true
		}
	}
	if !added {
		body, err := src.call(ctx, nmPath, nmName+".ActivateConnection", w.interactive(), conn, dev, ap)
		if err != nil {
			writeNMErr(rw, err)
			return
		}
		if len(body) == 1 {
			active, _ = body[0].(dbus.ObjectPath)
		}
	}
	ok, reason := waitActivated(ctx, src, active, failures)
	result := "connected"
	if !ok {
		result = reason
	}
	w.audit(r, protocol.AuditEvent{Event: "network_connect", Name: firstNonEmpty(req.SSID, d.Connection), Device: d.Interface, Result: result})
	if !ok && added && reason == "wrong_password" {
		// Not saved with a password that does not work: the next try asks again.
		if _, err := src.call(ctx, conn, nmSettingsConn+".Delete", false); err != nil {
			slog.Info("worker: delete failed connection", "err", err)
		}
	}
	writeJSON(rw, http.StatusOK, map[string]any{"connected": ok, "reason": reason})
}

// newWifiSettings is a new connection for a network, as nm-applet makes
// them: system-wide, the password stored by NetworkManager.
func newWifiSettings(n WifiNetwork, password string) (connSettings, error) {
	s := connSettings{
		"connection":      {"id": dbus.MakeVariant(n.SSID), "type": dbus.MakeVariant("802-11-wireless")},
		"802-11-wireless": {"ssid": dbus.MakeVariant([]byte(n.SSID)), "mode": dbus.MakeVariant("infrastructure")},
	}
	switch n.Security {
	case "none":
	case "psk", "sae":
		if password == "" {
			return nil, codedErr{http.StatusBadRequest, "password_required", "this network needs a password"}
		}
		mgmt := "wpa-psk"
		if n.Security == "sae" {
			mgmt = "sae"
		}
		s["802-11-wireless-security"] = map[string]dbus.Variant{
			"key-mgmt": dbus.MakeVariant(mgmt),
			"psk":      dbus.MakeVariant(password),
		}
	default:
		return nil, codedErr{http.StatusBadRequest, "unsupported", "this network's security needs the network settings, which Tilde does not have yet"}
	}
	return s, nil
}

// setPassword stores a new password in a saved connection. GetSettings
// leaves secrets out, so the update carries the settings back with the
// new password in.
func setPassword(ctx context.Context, src nmSource, conn dbus.ObjectPath, password string, interactive bool) error {
	s, err := src.settings(ctx, conn)
	if err != nil {
		return err
	}
	sec := s["802-11-wireless-security"]
	if sec == nil {
		return codedErr{http.StatusBadRequest, "unsupported", "this saved connection has no password"}
	}
	sec["psk"] = dbus.MakeVariant(password)
	// The deprecated address and route lists hold structs, which do not
	// come back with their D-Bus types; address-data and route-data say
	// the same in dictionaries, which do.
	for _, ip := range []string{"ipv4", "ipv6"} {
		delete(s[ip], "addresses")
		delete(s[ip], "routes")
	}
	_, err = src.call(ctx, conn, nmSettingsConn+".Update", interactive, s)
	return err
}

// NetworkManager's reasons for a device failing (NMDeviceStateReason) that
// mean the password was wrong: no secrets (none that worked), or the
// supplicant giving up during the handshake.
var wrongPasswordReasons = []uint32{7, 8, 11}

// waitActivated follows an activation until its connection is up, or gone
// (failed); reason says why it failed: wrong_password, failed or timeout.
// failures carries the device's failure reasons (watchFailures).
func waitActivated(ctx context.Context, src nmSource, active dbus.ObjectPath, failures <-chan uint32) (bool, string) {
	if active == "" || active == "/" {
		return false, "failed"
	}
	why := func(code uint32) string {
		if slices.Contains(wrongPasswordReasons, code) {
			return "wrong_password"
		}
		return "failed"
	}
	deadline := time.Now().Add(connectTimeout)
	for time.Now().Before(deadline) {
		select {
		case code := <-failures:
			return false, why(code)
		default:
		}
		v, err := src.get(ctx, active, nmActive, "State")
		state, _ := v.Value().(uint32)
		if err == nil && state == nmActiveActivated {
			return true, ""
		}
		if err != nil || state == nmActiveDeactivated {
			// Gone: the device's signal saying why may still be on its way.
			select {
			case code := <-failures:
				return false, why(code)
			case <-time.After(500 * time.Millisecond):
				return false, "failed"
			}
		}
		select {
		case <-ctx.Done():
			return false, "timeout"
		case <-time.After(250 * time.Millisecond):
		}
	}
	return false, "timeout"
}

// handleNetworkDisconnect takes a device down until it is connected again
// (NetworkManager does not bring it back up by itself).
func (w *Worker) handleNetworkDisconnect(rw http.ResponseWriter, r *http.Request) {
	var req struct {
		Device string `json:"device"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(rw, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), authTimeout+time.Minute)
	defer cancel()
	src, d, err := w.findNetDevice(ctx, req.Device)
	if err != nil {
		writeErr(rw, err)
		return
	}
	if _, err := src.call(ctx, dbus.ObjectPath(d.ID), nmDevice+".Disconnect", w.interactive()); err != nil {
		var de dbus.Error
		if !errors.As(err, &de) || !strings.HasSuffix(de.Name, ".NotActive") {
			writeNMErr(rw, err)
			return
		}
	}
	w.audit(r, protocol.AuditEvent{Event: "network_disconnect", Name: d.Connection, Device: d.Interface})
	rw.WriteHeader(http.StatusNoContent)
}
