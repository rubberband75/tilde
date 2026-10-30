// Packet names (D80). Xpra 7 renamed most packets ("new-window" became
// "window-create") and takes the old names as well, by default; Xpra 6
// knows only the old ones. Tilde speaks Xpra 6's, and reads a packet sent
// under its new name as the old one, so each handler has one name. From
// Xpra 7's xpra/net/packet_type.py.

const legacy = new Map<string, string>([
  ['window-create', 'new-window'],
  ['window-raise', 'raise-window'],
  ['window-restack', 'restack-window'],
  ['window-initiate-moveresize', 'initiate-moveresize'],
  ['window-destroy', 'lost-window'],
  ['window-draw', 'draw'],
  ['window-eos', 'eos'],
  ['window-grab', 'pointer-grab'],
  ['window-ungrab', 'pointer-ungrab'],
  ['window-bell', 'bell'],
  ['encoding-set', 'encodings'],
  ['cursor-set', 'set-cursors'],
  ['display-resized', 'desktop_size'],
  ['display-show-desktop', 'show-desktop'],
  ['display-ungrab', 'force-ungrab'],
  ['notification-show', 'notify_show'],
  ['notification-close', 'notify_close'],
  ['bell-set', 'set-bell'],
  ['events', 'server-event'],
  ['connection-close', 'disconnect'],
  ['ping-echo', 'ping_echo'],
])

/** The name Tilde handles a received packet by. */
export function packetName(wire: string): string {
  return legacy.get(wire) ?? wire
}
