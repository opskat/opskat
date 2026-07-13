package rdp_svc

import (
	"encoding/base64"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type framebufferStub struct {
	pix    []byte
	width  int
	height int
}

func (s framebufferStub) FramebufferDims() (int, int) { return s.width, s.height }

func (s framebufferStub) FramebufferWriteTopDown(dst []byte) (int, int) {
	if len(dst) < len(s.pix) {
		return 0, 0
	}
	copy(dst, s.pix)
	return s.width, s.height
}

// 4x4 RGBA framebuffer where every pixel's R channel encodes its index,
// so region extraction mistakes show up as wrong pixel values.
func testFramebuffer() framebufferStub {
	pix := make([]byte, 4*4*4)
	for i := range 16 {
		pix[i*4] = byte(i)
		pix[i*4+3] = 255
	}
	return framebufferStub{pix: pix, width: 4, height: 4}
}

func TestMarkDirtyMergesIntoBoundingBox(t *testing.T) {
	f := newFrameStreamer()

	f.markDirty(2, 3, 4, 5)
	f.markDirty(1, 1, 2, 2)

	rect, ok := f.takeDirty()
	require.True(t, ok)
	require.Equal(t, dirtyRect{x: 1, y: 1, w: 5, h: 7}, rect)
}

func TestMarkDirtyIgnoresEmptyRects(t *testing.T) {
	f := newFrameStreamer()

	f.markDirty(1, 1, 0, 5)
	f.markDirty(1, 1, 5, -1)

	_, ok := f.takeDirty()
	require.False(t, ok)
}

func TestTakeDirtyClearsBox(t *testing.T) {
	f := newFrameStreamer()
	f.markDirty(0, 0, 2, 2)

	_, ok := f.takeDirty()
	require.True(t, ok)
	_, ok = f.takeDirty()
	require.False(t, ok)
}

func TestFlushIntervalScalesWithDirtyArea(t *testing.T) {
	tiny := flushInterval(dirtyRect{w: 19, h: 10}, 1920, 1080)
	full := flushInterval(dirtyRect{w: 1920, h: 1080}, 1920, 1080)

	require.Less(t, tiny, 20*time.Millisecond, "small updates should flush at ~60fps")
	require.Greater(t, full, 100*time.Millisecond, "full-frame updates must stay throttled")
	require.Less(t, full, 150*time.Millisecond)
}

func TestBuildFrameEventEncodesDirtyRegion(t *testing.T) {
	f := newFrameStreamer()
	fb := testFramebuffer()

	ev, ok := f.buildFrameEvent(fb, dirtyRect{x: 1, y: 1, w: 2, h: 2}, "sess-1")

	require.True(t, ok)
	require.Equal(t, "frame", ev.Type)
	require.Equal(t, "sess-1", ev.SessionID)
	require.Equal(t, 4, ev.Width)
	require.Equal(t, 4, ev.Height)
	require.Equal(t, 1, ev.X)
	require.Equal(t, 1, ev.Y)
	require.Equal(t, 2, ev.RectWidth)
	require.Equal(t, 2, ev.RectHeight)

	got, err := base64.StdEncoding.DecodeString(ev.Data)
	require.NoError(t, err)
	// Rows y=1..2, columns x=1..2 of the index-encoded framebuffer.
	want := []byte{
		5, 0, 0, 255, 6, 0, 0, 255,
		9, 0, 0, 255, 10, 0, 0, 255,
	}
	require.Equal(t, want, got)
}

func TestBuildFrameEventFullFrame(t *testing.T) {
	f := newFrameStreamer()
	fb := testFramebuffer()

	ev, ok := f.buildFrameEvent(fb, dirtyRect{x: 0, y: 0, w: 4, h: 4}, "sess-1")

	require.True(t, ok)
	require.Equal(t, 0, ev.X)
	require.Equal(t, 4, ev.RectWidth)
	require.Equal(t, 4, ev.RectHeight)
	got, err := base64.StdEncoding.DecodeString(ev.Data)
	require.NoError(t, err)
	require.Equal(t, fb.pix, got)
}

func TestBuildFrameEventClampsRectToFramebuffer(t *testing.T) {
	f := newFrameStreamer()
	fb := testFramebuffer()

	// Dirty rect recorded before a shrink can exceed current dims.
	ev, ok := f.buildFrameEvent(fb, dirtyRect{x: 2, y: 2, w: 10, h: 10}, "sess-1")

	require.True(t, ok)
	require.Equal(t, 2, ev.X)
	require.Equal(t, 2, ev.Y)
	require.Equal(t, 2, ev.RectWidth)
	require.Equal(t, 2, ev.RectHeight)
}

func TestBuildFrameEventRectOutsideFramebuffer(t *testing.T) {
	f := newFrameStreamer()
	fb := testFramebuffer()

	_, ok := f.buildFrameEvent(fb, dirtyRect{x: 10, y: 10, w: 2, h: 2}, "sess-1")

	require.False(t, ok)
}

func TestBuildFrameEventEmptyFramebuffer(t *testing.T) {
	f := newFrameStreamer()

	_, ok := f.buildFrameEvent(framebufferStub{}, dirtyRect{x: 0, y: 0, w: 2, h: 2}, "sess-1")

	require.False(t, ok)
}
