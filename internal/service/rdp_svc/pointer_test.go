package rdp_svc

import (
	"encoding/base64"
	"testing"

	rdp "github.com/bouncyball-git/gopher-rdp"
	"github.com/stretchr/testify/require"
)

func TestPointerEventShape(t *testing.T) {
	rgba := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	ev := pointerEvent("sess-1", &rdp.PointerUpdate{
		Type:     rdp.PointerShape,
		HotSpotX: 3,
		HotSpotY: 5,
		Width:    2,
		Height:   1,
		Data:     rgba,
	})

	require.Equal(t, Event{
		Type:        "pointer",
		SessionID:   "sess-1",
		PointerType: "shape",
		HotspotX:    3,
		HotspotY:    5,
		Width:       2,
		Height:      1,
		Data:        base64.StdEncoding.EncodeToString(rgba),
	}, ev)
}

func TestPointerEventNullHidesCursor(t *testing.T) {
	ev := pointerEvent("sess-1", &rdp.PointerUpdate{Type: rdp.PointerNull})

	require.Equal(t, Event{Type: "pointer", SessionID: "sess-1", PointerType: "hidden"}, ev)
}

func TestPointerEventDefault(t *testing.T) {
	ev := pointerEvent("sess-1", &rdp.PointerUpdate{Type: rdp.PointerDefault})

	require.Equal(t, Event{Type: "pointer", SessionID: "sess-1", PointerType: "default"}, ev)
}

func TestPointerEventCacheMissFallsBackToDefault(t *testing.T) {
	// The library resolves cached pointers to full shape updates; a raw
	// PointerCached only reaches the callback on a cache miss, which carries
	// no pixel data — the safe rendering is the default cursor.
	ev := pointerEvent("sess-1", &rdp.PointerUpdate{Type: rdp.PointerCached, CacheIndex: 9})

	require.Equal(t, Event{Type: "pointer", SessionID: "sess-1", PointerType: "default"}, ev)
}
