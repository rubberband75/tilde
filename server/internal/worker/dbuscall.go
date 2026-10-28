package worker

import (
	"context"
	"strings"

	"github.com/godbus/dbus/v5"
)

// godbus's Object.Call keeps only the NoAutoStart and NoReplyExpected
// flags of a call (object.go: msg.Flags = flags & ...), so its
// FlagAllowInteractiveAuthorization never reaches the bus. Services that
// leave it to the caller whether polkit may ask for a password (systemd's
// unit-file methods) then never ask. These calls build the message
// themselves (D62).

// methodCall is the message for a method call; interactive lets the
// service have polkit ask for a password, through Tilde's agent (D44).
func methodCall(dest string, path dbus.ObjectPath, method string, interactive bool, args ...any) *dbus.Message {
	iface, member := "", method
	if i := strings.LastIndex(method, "."); i >= 0 {
		iface, member = method[:i], method[i+1:]
	}
	msg := &dbus.Message{
		Type: dbus.TypeMethodCall,
		Headers: map[dbus.HeaderField]dbus.Variant{
			dbus.FieldPath:        dbus.MakeVariant(path),
			dbus.FieldDestination: dbus.MakeVariant(dest),
			dbus.FieldMember:      dbus.MakeVariant(member),
		},
	}
	if iface != "" {
		msg.Headers[dbus.FieldInterface] = dbus.MakeVariant(iface)
	}
	if interactive {
		msg.Flags |= dbus.FlagAllowInteractiveAuthorization
	}
	if len(args) > 0 {
		msg.Body = args
		msg.Headers[dbus.FieldSignature] = dbus.MakeVariant(dbus.SignatureOf(args...))
	}
	return msg
}

// callWith sends a method call and waits for its reply.
func callWith(ctx context.Context, conn *dbus.Conn, dest string, path dbus.ObjectPath, method string, interactive bool, args ...any) *dbus.Call {
	call := conn.SendWithContext(ctx, methodCall(dest, path, method, interactive, args...), make(chan *dbus.Call, 1))
	if call.Err != nil {
		return call
	}
	select {
	case done := <-call.Done:
		return done
	case <-ctx.Done():
		return &dbus.Call{Err: ctx.Err()}
	}
}
