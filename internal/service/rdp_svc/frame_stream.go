package rdp_svc

import (
	"encoding/base64"
	"sync"
	"time"
)

// Frame flush pacing: small dirty regions stream at up to ~60fps, and the
// interval grows linearly with the dirty area so a full-screen change is
// throttled to roughly 9fps — bounding the worst-case payload bandwidth.
const (
	frameTickInterval   = 16 * time.Millisecond
	frameFlushFloor     = 12 * time.Millisecond
	frameFlushAreaScale = 100 * time.Millisecond
)

type dirtyRect struct {
	x, y, w, h int
}

// frameStreamer coalesces bitmap-update callbacks into a dirty bounding box
// and turns it into incremental frame events read from the framebuffer.
// markDirty is called from the RDP receive goroutine and must stay O(1);
// the flush loop (frameLoop) drains it and does the copying/encoding.
type frameStreamer struct {
	mu       sync.Mutex
	dirty    dirtyRect
	hasDirty bool

	fbBuf   []byte // reused full-framebuffer snapshot
	rectBuf []byte // reused dirty-region extraction buffer
}

func newFrameStreamer() *frameStreamer {
	return &frameStreamer{}
}

func (f *frameStreamer) markDirty(x, y, w, h int) {
	if w <= 0 || h <= 0 {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.hasDirty {
		f.dirty = dirtyRect{x: x, y: y, w: w, h: h}
		f.hasDirty = true
		return
	}
	x2 := max(f.dirty.x+f.dirty.w, x+w)
	y2 := max(f.dirty.y+f.dirty.h, y+h)
	f.dirty.x = min(f.dirty.x, x)
	f.dirty.y = min(f.dirty.y, y)
	f.dirty.w = x2 - f.dirty.x
	f.dirty.h = y2 - f.dirty.y
}

func (f *frameStreamer) takeDirty() (dirtyRect, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.hasDirty {
		return dirtyRect{}, false
	}
	rect := f.dirty
	f.hasDirty = false
	f.dirty = dirtyRect{}
	return rect, true
}

// flushInterval returns the minimum delay before the next flush after sending
// rect, scaling linearly from ~60fps for tiny updates to ~9fps for full frames.
func flushInterval(rect dirtyRect, fbW, fbH int) time.Duration {
	if fbW <= 0 || fbH <= 0 {
		return frameFlushFloor
	}
	frac := float64(rect.w*rect.h) / float64(fbW*fbH)
	if frac > 1 {
		frac = 1
	}
	return frameFlushFloor + time.Duration(frac*float64(frameFlushAreaScale))
}

// buildFrameEvent snapshots the framebuffer and encodes the dirty region as a
// frame event. Returns false when the framebuffer is unavailable or the rect
// falls entirely outside it (e.g. recorded before a shrink).
func (f *frameStreamer) buildFrameEvent(src framebufferSource, rect dirtyRect, sessionID string) (Event, bool) {
	w, h := src.FramebufferDims()
	if w <= 0 || h <= 0 {
		return Event{}, false
	}
	need := w * h * 4
	if cap(f.fbBuf) < need {
		f.fbBuf = make([]byte, need)
	}
	f.fbBuf = f.fbBuf[:need]
	if fw, fh := src.FramebufferWriteTopDown(f.fbBuf); fw != w || fh != h {
		return Event{}, false
	}

	rect = clampRect(rect, w, h)
	if rect.w <= 0 || rect.h <= 0 {
		return Event{}, false
	}

	region := f.fbBuf
	if rect.w != w || rect.h != h {
		rowLen := rect.w * 4
		size := rowLen * rect.h
		if cap(f.rectBuf) < size {
			f.rectBuf = make([]byte, size)
		}
		f.rectBuf = f.rectBuf[:size]
		fbStride := w * 4
		for row := 0; row < rect.h; row++ {
			srcOff := (rect.y+row)*fbStride + rect.x*4
			copy(f.rectBuf[row*rowLen:(row+1)*rowLen], f.fbBuf[srcOff:srcOff+rowLen])
		}
		region = f.rectBuf
	}

	return Event{
		Type:       "frame",
		SessionID:  sessionID,
		Width:      w,
		Height:     h,
		X:          rect.x,
		Y:          rect.y,
		RectWidth:  rect.w,
		RectHeight: rect.h,
		Data:       base64.StdEncoding.EncodeToString(region),
	}, true
}

func clampRect(rect dirtyRect, fbW, fbH int) dirtyRect {
	if rect.x < 0 {
		rect.w += rect.x
		rect.x = 0
	}
	if rect.y < 0 {
		rect.h += rect.y
		rect.y = 0
	}
	rect.w = min(rect.w, fbW-rect.x)
	rect.h = min(rect.h, fbH-rect.y)
	return rect
}
