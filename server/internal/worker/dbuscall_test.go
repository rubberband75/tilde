package worker

import (
	"testing"

	"github.com/godbus/dbus/v5"
)

// TestMethodCallKeepsInteractive: the flag godbus's Object.Call drops is
// on the message, with the headers a method call needs.
func TestMethodCallKeepsInteractive(t *testing.T) {
	msg := methodCall(systemdName, systemdPath, systemdManager+".EnableUnitFilesWithFlags", true, []string{"cups.service"}, uint64(0))
	if msg.Flags&dbus.FlagAllowInteractiveAuthorization == 0 {
		t.Fatal("not interactive")
	}
	want := map[dbus.HeaderField]any{
		dbus.FieldPath:        dbus.ObjectPath(systemdPath),
		dbus.FieldDestination: systemdName,
		dbus.FieldInterface:   systemdManager,
		dbus.FieldMember:      "EnableUnitFilesWithFlags",
		dbus.FieldSignature:   dbus.ParseSignatureMust("ast"),
	}
	for f, v := range want {
		if got := msg.Headers[f].Value(); got != v {
			t.Errorf("header %d = %v, want %v", f, got, v)
		}
	}
	if err := msg.IsValid(); err != nil {
		t.Fatal(err)
	}
	if plain := methodCall(nmName, nmPath, "org.freedesktop.DBus.Properties.Get", false, "a", "b"); plain.Flags != 0 {
		t.Fatalf("flags %v", plain.Flags)
	}
}
