package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

// fakeNM stands in for NetworkManager: its objects, saved connections, and
// what activating does (activate: the reason an activation fails with, or 0
// for success).
type fakeNM struct {
	mu       sync.Mutex
	objs     managedObjects
	saved    map[dbus.ObjectPath]connSettings
	failWith uint32
	calls    []string
	updates  []connSettings
	seq      int
	// failed is how NetworkManager's StateChanged signal reaches the worker.
	failed func(dev dbus.ObjectPath, reason uint32)
}

func (f *fakeNM) objects(context.Context) (managedObjects, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.objs, nil
}

func (f *fakeNM) settings(_ context.Context, conn dbus.ObjectPath) (connSettings, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.saved[conn]
	if !ok {
		return nil, dbus.Error{Name: nmErrPrefix + "Settings.PermissionDenied"}
	}
	// A copy without secrets, as NetworkManager gives.
	out := connSettings{}
	for k, m := range s {
		out[k] = map[string]dbus.Variant{}
		for kk, vv := range m {
			if kk != "psk" {
				out[k][kk] = vv
			}
		}
	}
	return out, nil
}

func (f *fakeNM) get(_ context.Context, path dbus.ObjectPath, iface, name string) (dbus.Variant, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	props, ok := f.objs[path][iface]
	if !ok {
		return dbus.Variant{}, dbus.Error{Name: "org.freedesktop.DBus.Error.UnknownObject"}
	}
	return props[name], nil
}

func (f *fakeNM) call(_ context.Context, path dbus.ObjectPath, method string, interactive bool, args ...any) ([]any, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	name := method[strings.LastIndex(method, ".")+1:]
	f.calls = append(f.calls, fmt.Sprintf("%s %s interactive=%v", path, name, interactive))
	switch name {
	case "Set":
		f.objs[nmPath][nmName][args[1].(string)] = args[2].(dbus.Variant)
	case "AddAndActivateConnection":
		f.seq++
		conn := dbus.ObjectPath(fmt.Sprintf("/org/freedesktop/NetworkManager/Settings/%d", 100+f.seq))
		f.saved[conn] = args[0].(connSettings)
		f.objs[conn] = map[string]map[string]dbus.Variant{nmSettingsConn: {}}
		return []any{conn, f.activate(args[1].(dbus.ObjectPath))}, nil
	case "ActivateConnection":
		return []any{f.activate(args[1].(dbus.ObjectPath))}, nil
	case "Update":
		f.updates = append(f.updates, args[0].(connSettings))
	case "Delete":
		delete(f.saved, path)
		delete(f.objs, path)
	case "Disconnect", "RequestScan":
	default:
		return nil, dbus.Error{Name: "test.UnknownMethod"}
	}
	return nil, nil
}

// activate makes an active connection that is up, or one already gone with
// the device saying why.
func (f *fakeNM) activate(dev dbus.ObjectPath) dbus.ObjectPath {
	f.seq++
	active := dbus.ObjectPath(fmt.Sprintf("/org/freedesktop/NetworkManager/ActiveConnection/%d", 100+f.seq))
	if f.failWith != 0 {
		// Failed, and already on to "disconnected" by the time anyone looks.
		f.objs[dev][nmDevice]["StateReason"] = v([]any{uint32(nmStateDisconnected), uint32(0)})
		go f.failed(dev, f.failWith)
		return active
	}
	f.objs[active] = map[string]map[string]dbus.Variant{nmActive: {"State": v(uint32(nmActiveActivated)), "Id": v("new")}}
	return active
}

const (
	devEth  = "/org/freedesktop/NetworkManager/Devices/1"
	devWifi = "/org/freedesktop/NetworkManager/Devices/2"
	devLo   = "/org/freedesktop/NetworkManager/Devices/3"
	devBr   = "/org/freedesktop/NetworkManager/Devices/4"
	devUnm  = "/org/freedesktop/NetworkManager/Devices/5"
	acEth   = "/org/freedesktop/NetworkManager/ActiveConnection/1"
	acWifi  = "/org/freedesktop/NetworkManager/ActiveConnection/2"
	ip4Eth  = "/org/freedesktop/NetworkManager/IP4Config/1"
	ip6Eth  = "/org/freedesktop/NetworkManager/IP6Config/1"
	savHome = "/org/freedesktop/NetworkManager/Settings/1"
	savWire = "/org/freedesktop/NetworkManager/Settings/2"
)

func ap(ssid string, strength byte, flags, wpa, rsn uint32) map[string]map[string]dbus.Variant {
	return map[string]map[string]dbus.Variant{nmAccessPoint: {
		"Ssid": v([]byte(ssid)), "Strength": v(strength), "Flags": v(flags), "WpaFlags": v(wpa), "RsnFlags": v(rsn),
	}}
}

func apPath(n int) dbus.ObjectPath {
	return dbus.ObjectPath(fmt.Sprintf("/org/freedesktop/NetworkManager/AccessPoint/%d", n))
}

func device(iface string, kind, state uint32, active dbus.ObjectPath) map[string]map[string]dbus.Variant {
	return map[string]map[string]dbus.Variant{nmDevice: {
		"Interface": v(iface), "DeviceType": v(kind), "State": v(state), "ActiveConnection": v(active),
		"Ip4Config": v(dbus.ObjectPath("/")), "Ip6Config": v(dbus.ObjectPath("/")),
	}}
}

func addressData(addrs ...string) dbus.Variant {
	var out []map[string]dbus.Variant
	for _, a := range addrs {
		out = append(out, map[string]dbus.Variant{"address": v(a), "prefix": v(uint32(24))})
	}
	return v(out)
}

func testNM() *fakeNM {
	psk := uint32(apKeyPSK | 0x8) // PSK, CCMP
	objs := managedObjects{
		nmPath: {nmName: {"WirelessEnabled": v(true), "WirelessHardwareEnabled": v(true)}},
		devEth: device("enp3s0", nmTypeEthernet, nmStateActivated, acEth),
		devLo:  device("lo", 32, nmStateActivated, "/"),
		devBr:  device("docker0", 13, nmStateActivated, "/"),
		devUnm: device("enp4s0", nmTypeEthernet, nmStateUnmanaged, "/"),
		acEth:  {nmActive: {"Id": v("Wired connection 1"), "Default": v(true), "State": v(uint32(nmActiveActivated))}},
		acWifi: {nmActive: {"Id": v("HomeNet"), "State": v(uint32(nmActiveActivated))}},
		ip4Eth: {nmIP4: {"AddressData": addressData("192.168.1.20")}},
		ip6Eth: {nmIP6: {"AddressData": addressData("fe80::1", "2001:db8::20")}},
		// Home has two access points; the stronger one counts.
		apPath(1): ap("HomeNet", 40, apPrivacy, 0, psk),
		apPath(2): ap("HomeNet", 70, apPrivacy, 0, psk),
		apPath(3): ap("Cafe", 55, 0, 0, 0),
		apPath(4): ap("Office", 90, apPrivacy, 0, apKey8021X),
		apPath(5): ap("", 80, apPrivacy, 0, psk), // hidden
		apPath(6): ap("Modern", 30, apPrivacy, 0, apKeySAE),
		apPath(7): ap("Mixed", 20, apPrivacy, 0, apKeySAE|apKeyPSK),
		savHome:   {nmSettingsConn: {}},
		savWire:   {nmSettingsConn: {}},
		"/org/freedesktop/NetworkManager/Settings/3": {nmSettingsConn: {}}, // not the user's to see
	}
	objs[devEth][nmDevice]["Ip4Config"] = v(dbus.ObjectPath(ip4Eth))
	objs[devEth][nmDevice]["Ip6Config"] = v(dbus.ObjectPath(ip6Eth))
	objs[devWifi] = device("wlp2s0", nmTypeWifi, nmStateActivated, acWifi)
	objs[devWifi][nmWireless] = map[string]dbus.Variant{
		"AccessPoints":      v([]dbus.ObjectPath{apPath(1), apPath(2), apPath(3), apPath(4), apPath(5), apPath(6), apPath(7)}),
		"ActiveAccessPoint": v(apPath(1)),
	}
	return &fakeNM{objs: objs, saved: map[dbus.ObjectPath]connSettings{
		savHome: {
			"connection":               {"id": v("HomeNet"), "type": v("802-11-wireless")},
			"802-11-wireless":          {"ssid": v([]byte("HomeNet"))},
			"802-11-wireless-security": {"key-mgmt": v("wpa-psk"), "psk": v("old")},
			"ipv4": {
				"method":       v("manual"),
				"addresses":    v([][]uint32{{0x1401a8c0, 24, 0x0101a8c0}}),
				"address-data": addressData("192.168.1.20"),
			},
		},
		savWire: {"connection": {"id": v("Wired connection 1"), "type": v("802-3-ethernet")}},
	}}
}

func networkWorker(t *testing.T, f *fakeNM) *Worker {
	t.Helper()
	w := driveWorker(t, nil)
	if f != nil {
		w.network.src = f
		f.failed = w.network.deviceFailed
	} else {
		w.network.failedAt = time.Now() // no NetworkManager: do not try the real bus
	}
	return w
}

func readState(t *testing.T, w *Worker) NetworkState {
	t.Helper()
	rec := serve(t, w, "GET", "/api/v1/network", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("network: %d %s", rec.Code, rec.Body)
	}
	var st NetworkState
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	return st
}

func TestNetworkState(t *testing.T) {
	st := readState(t, networkWorker(t, testNM()))
	if !st.Available || !st.WifiEnabled || !st.WifiHardware {
		t.Fatalf("state = %+v", st)
	}
	if len(st.Devices) != 2 {
		t.Fatalf("devices = %+v (loopback, bridges and unmanaged devices are left out)", st.Devices)
	}
	eth, wifi := st.Devices[0], st.Devices[1]
	want := NetDevice{
		ID: devEth, Interface: "enp3s0", Type: "ethernet", State: "connected",
		Connection: "Wired connection 1", Addresses: []string{"192.168.1.20", "2001:db8::20"}, Default: true,
	}
	if !reflect.DeepEqual(eth, want) {
		t.Fatalf("ethernet:\n got %+v\nwant %+v", eth, want)
	}
	if wifi.Type != "wifi" || wifi.Connection != "HomeNet" || wifi.Default {
		t.Fatalf("wifi = %+v", wifi)
	}
	var rows []string
	for _, n := range wifi.Networks {
		rows = append(rows, fmt.Sprintf("%s %d %s active=%v known=%v", n.SSID, n.Strength, n.Security, n.Active, n.Known))
	}
	wantRows := []string{
		"HomeNet 70 psk active=true known=true",
		"Office 90 enterprise active=false known=false",
		"Cafe 55 none active=false known=false",
		"Modern 30 sae active=false known=false",
		"Mixed 20 psk active=false known=false",
	}
	if !reflect.DeepEqual(rows, wantRows) {
		t.Fatalf("networks:\n got %q\nwant %q", rows, wantRows)
	}
}

func TestNetworkWithoutNetworkManager(t *testing.T) {
	w := networkWorker(t, nil)
	st := readState(t, w)
	if st.Available || st.Devices == nil || len(st.Devices) != 0 {
		t.Fatalf("state = %+v", st)
	}
	rec := serve(t, w, "POST", "/api/v1/network/disconnect", `{"device":"`+devEth+`"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("disconnect without NetworkManager: %d", rec.Code)
	}
}

func TestNetworkConnectNewNetwork(t *testing.T) {
	f := testNM()
	w := networkWorker(t, f)
	connect := func(body string) (int, map[string]any) {
		rec := serve(t, w, "POST", "/api/v1/network/connect", body)
		var res map[string]any
		json.Unmarshal(rec.Body.Bytes(), &res)
		return rec.Code, res
	}

	code, res := connect(`{"device":"` + devWifi + `","ssid":"Modern"}`)
	if code != http.StatusBadRequest || res["code"] != "password_required" {
		t.Fatalf("no password: %d %v", code, res)
	}
	code, res = connect(`{"device":"` + devWifi + `","ssid":"Office","password":"x"}`)
	if code != http.StatusBadRequest || res["code"] != "unsupported" {
		t.Fatalf("enterprise: %d %v", code, res)
	}
	code, res = connect(`{"device":"` + devWifi + `","ssid":"Nowhere"}`)
	if code != http.StatusBadRequest {
		t.Fatalf("out of range: %d %v", code, res)
	}
	code, res = connect(`{"device":"` + devLo + `"}`)
	if code != http.StatusBadRequest {
		t.Fatalf("loopback: %d %v", code, res)
	}

	code, res = connect(`{"device":"` + devWifi + `","ssid":"Modern","password":"sesame"}`)
	if code != http.StatusOK || res["connected"] != true {
		t.Fatalf("connect: %d %v", code, res)
	}
	var added connSettings
	for p, s := range f.saved {
		if p != savHome && p != savWire {
			added = s
		}
	}
	sec := added["802-11-wireless-security"]
	if string(prop[[]byte](added["802-11-wireless"], "ssid")) != "Modern" ||
		prop[string](sec, "key-mgmt") != "sae" || prop[string](sec, "psk") != "sesame" {
		t.Fatalf("added connection = %v", added)
	}
	last := f.calls[len(f.calls)-1]
	if last != nmPath+" AddAndActivateConnection interactive=false" {
		t.Fatalf("call = %q (polkit may only ask with an agent running)", last)
	}

	// An open network needs no password.
	if code, res = connect(`{"device":"` + devWifi + `","ssid":"Cafe"}`); res["connected"] != true {
		t.Fatalf("open network: %d %v", code, res)
	}
}

func TestNetworkConnectWrongPassword(t *testing.T) {
	f := testNM()
	f.failWith = 7 // no secrets
	w := networkWorker(t, f)
	rec := serve(t, w, "POST", "/api/v1/network/connect", `{"device":"`+devWifi+`","ssid":"Mixed","password":"wrong"}`)
	var res map[string]any
	json.Unmarshal(rec.Body.Bytes(), &res)
	if rec.Code != http.StatusOK || res["connected"] != false || res["reason"] != "wrong_password" {
		t.Fatalf("wrong password: %d %v", rec.Code, res)
	}
	// Not kept with a password that does not work.
	if len(f.saved) != 2 || !slices.ContainsFunc(f.calls, func(c string) bool { return strings.HasSuffix(c, " Delete interactive=false") }) {
		t.Fatalf("saved = %v, calls = %v", f.saved, f.calls)
	}

	f.failWith = 2 // some other failure
	rec = serve(t, w, "POST", "/api/v1/network/connect", `{"device":"`+devWifi+`","ssid":"Cafe"}`)
	json.Unmarshal(rec.Body.Bytes(), &res)
	if res["reason"] != "failed" {
		t.Fatalf("failure: %v", res)
	}
}

func TestNetworkConnectSavedNetwork(t *testing.T) {
	f := testNM()
	w := networkWorker(t, f)
	rec := serve(t, w, "POST", "/api/v1/network/connect", `{"device":"`+devWifi+`","ssid":"HomeNet"}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"connected":true`) {
		t.Fatalf("connect: %d %s", rec.Code, rec.Body)
	}
	if len(f.updates) != 0 || !slices.Contains(f.calls, nmPath+" ActivateConnection interactive=false") {
		t.Fatalf("calls = %v", f.calls)
	}

	// A new password goes into the saved connection, the rest kept.
	serve(t, w, "POST", "/api/v1/network/connect", `{"device":"`+devWifi+`","ssid":"HomeNet","password":"new"}`)
	if len(f.updates) != 1 {
		t.Fatalf("updates = %v", f.updates)
	}
	u := f.updates[0]
	if prop[string](u["802-11-wireless-security"], "psk") != "new" || prop[string](u["connection"], "id") != "HomeNet" {
		t.Fatalf("update = %v", u)
	}
	// The deprecated list goes; the dictionaries saying the same stay.
	if _, ok := u["ipv4"]["addresses"]; ok || u["ipv4"]["address-data"].Value() == nil {
		t.Fatalf("update ipv4 = %v", u["ipv4"])
	}

	// Wired: NetworkManager picks the connection.
	rec = serve(t, w, "POST", "/api/v1/network/connect", `{"device":"`+devEth+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("wired: %d %s", rec.Code, rec.Body)
	}
}

func TestNetworkSwitches(t *testing.T) {
	f := testNM()
	w := networkWorker(t, f)
	if rec := serve(t, w, "POST", "/api/v1/network/wifi", `{"enabled":false}`); rec.Code != http.StatusNoContent {
		t.Fatalf("wifi off: %d %s", rec.Code, rec.Body)
	}
	if readState(t, w).WifiEnabled {
		t.Fatal("wifi still on")
	}
	if rec := serve(t, w, "POST", "/api/v1/network/disconnect", `{"device":"`+devEth+`"}`); rec.Code != http.StatusNoContent {
		t.Fatalf("disconnect: %d %s", rec.Code, rec.Body)
	}
	if rec := serve(t, w, "POST", "/api/v1/network/scan", `{"device":"`+devWifi+`"}`); rec.Code != http.StatusNoContent {
		t.Fatalf("scan: %d %s", rec.Code, rec.Body)
	}
	want := []string{
		nmPath + " Set interactive=false",
		devEth + " Disconnect interactive=false",
		devWifi + " RequestScan interactive=false",
	}
	if !reflect.DeepEqual(f.calls, want) {
		t.Fatalf("calls = %v, want %v", f.calls, want)
	}
}

func TestNetworkPermissionDenied(t *testing.T) {
	f := &deniedNM{testNM()}
	w := driveWorker(t, nil)
	w.network.src = f
	rec := serve(t, w, "POST", "/api/v1/network/disconnect", `{"device":"`+devEth+`"}`)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "not_authorized") {
		t.Fatalf("denied: %d %s", rec.Code, rec.Body)
	}
}

// deniedNM refuses every change, as polkit does without a password.
type deniedNM struct{ *fakeNM }

func (d *deniedNM) call(context.Context, dbus.ObjectPath, string, bool, ...any) ([]any, error) {
	return nil, dbus.Error{Name: nmErrPrefix + "PermissionDenied"}
}

func TestAPSecurity(t *testing.T) {
	for _, tc := range []struct {
		flags, wpa, rsn uint32
		want            string
	}{
		{0, 0, 0, "none"},
		{apPrivacy, 0, 0, "other"}, // WEP
		{apPrivacy, apKeyPSK, 0, "psk"},
		{apPrivacy, 0, apKeyPSK, "psk"},
		{apPrivacy, 0, apKeySAE, "sae"},
		{apPrivacy, 0, apKeySAE | apKeyPSK, "psk"},
		{apPrivacy, 0, apKey8021X, "enterprise"},
		{0, 0, apKeyOWE, "other"},
	} {
		if got := apSecurity(ap("x", 0, tc.flags, tc.wpa, tc.rsn)[nmAccessPoint]); got != tc.want {
			t.Errorf("flags %x/%x/%x: %s, want %s", tc.flags, tc.wpa, tc.rsn, got, tc.want)
		}
	}
}
