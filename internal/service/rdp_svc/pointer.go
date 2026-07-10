package rdp_svc

import (
	"encoding/base64"

	rdp "github.com/bouncyball-git/gopher-rdp"
)

// pointerEvent converts a gopher-rdp pointer (cursor) update into the event
// emitted to the frontend, which renders it as the CSS cursor of the canvas.
func pointerEvent(sessionID string, u *rdp.PointerUpdate) Event {
	ev := Event{Type: "pointer", SessionID: sessionID}
	switch u.Type {
	case rdp.PointerNull:
		ev.PointerType = "hidden"
	case rdp.PointerShape:
		ev.PointerType = "shape"
		ev.HotspotX = int(u.HotSpotX)
		ev.HotspotY = int(u.HotSpotY)
		ev.Width = int(u.Width)
		ev.Height = int(u.Height)
		ev.Data = base64.StdEncoding.EncodeToString(u.Data)
	default:
		// PointerDefault, and PointerCached on a cache miss (the library
		// resolves cache hits to full shape updates) — no pixel data, so the
		// safe rendering is the default cursor.
		ev.PointerType = "default"
	}
	return ev
}
