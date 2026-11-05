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

// maxDirtyRects bounds the tracked rectangles so markDirty stays O(maxDirtyRects).
const maxDirtyRects = 8

func (r dirtyRect) area() int { return r.w * r.h }

func (r dirtyRect) union(o dirtyRect) dirtyRect {
	x, y := min(r.x, o.x), min(r.y, o.y)
	return dirtyRect{x: x, y: y, w: max(r.x+r.w, o.x+o.w) - x, h: max(r.y+r.h, o.y+o.h) - y}
}

// frameStreamer coalesces bitmap-update callbacks into a few dirty rectangles
// and turns each into an incremental frame event read from the framebuffer.
// markDirty is called from the RDP receive goroutine and must stay cheap;
// the flush loop (frameLoop) drains it and does the copying/encoding.
type frameStreamer struct {
	mu    sync.Mutex
	dirty []dirtyRect // at most maxDirtyRects, none empty

	rectBuf []byte // reused dirty-region extraction buffer
}

func newFrameStreamer() *frameStreamer {
	return &frameStreamer{}
}

// markDirty records a changed region. Rects are merged only when the union is
// no larger than the two parts together (overlap/adjacency), or, once the cap
// is exceeded, the pair whose union wastes the least area.
func (f *frameStreamer) markDirty(x, y, w, h int) {
	if w <= 0 || h <= 0 {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	cur := dirtyRect{x: x, y: y, w: w, h: h}
	for merged := true; merged; {
		merged = false
		for i, r := range f.dirty {
			if u := r.union(cur); u.area() <= r.area()+cur.area() {
				cur = u
				f.dirty = append(f.dirty[:i], f.dirty[i+1:]...)
				merged = true
				break
			}
		}
	}
	f.dirty = append(f.dirty, cur)
	if len(f.dirty) > maxDirtyRects {
		f.mergeCheapestPair()
	}
}

// mergeCheapestPair unions the two rects whose bounding box adds the least
// area. Called with f.mu held.
func (f *frameStreamer) mergeCheapestPair() {
	bi, bj, best := 0, 1, -1
	for i := range f.dirty {
		for j := i + 1; j < len(f.dirty); j++ {
			waste := f.dirty[i].union(f.dirty[j]).area() - f.dirty[i].area() - f.dirty[j].area()
			if best < 0 || waste < best {
				bi, bj, best = i, j, waste
			}
		}
	}
	f.dirty[bi] = f.dirty[bi].union(f.dirty[bj])
	f.dirty = append(f.dirty[:bj], f.dirty[bj+1:]...)
}

// takeDirty drains the tracked rects; nil when nothing is dirty.
func (f *frameStreamer) takeDirty() []dirtyRect {
	f.mu.Lock()
	defer f.mu.Unlock()
	rects := f.dirty
	f.dirty = nil
	return rects
}

// flushInterval returns the minimum delay before the next flush after sending
// dirtyArea pixels, scaling linearly from ~60fps for tiny updates to ~9fps for
// full frames.
func flushInterval(dirtyArea, fbW, fbH int) time.Duration {
	if fbW <= 0 || fbH <= 0 {
		return frameFlushFloor
	}
	frac := float64(dirtyArea) / float64(fbW*fbH)
	if frac > 1 {
		frac = 1
	}
	return frameFlushFloor + time.Duration(frac*float64(frameFlushAreaScale))
}

// buildFrameEvent copies only the dirty region out of the framebuffer and
// encodes it as a frame event. Returns false when the framebuffer is
// unavailable, resized since FramebufferDims, or the rect falls entirely
// outside it (e.g. recorded before a shrink).
func (f *frameStreamer) buildFrameEvent(src framebufferSource, rect dirtyRect, sessionID string) (Event, bool) {
	w, h := src.FramebufferDims()
	if w <= 0 || h <= 0 {
		return Event{}, false
	}
	rect = clampRect(rect, w, h)
	if rect.w <= 0 || rect.h <= 0 {
		return Event{}, false
	}
	size := rect.w * rect.h * 4
	if cap(f.rectBuf) < size {
		f.rectBuf = make([]byte, size)
	}
	f.rectBuf = f.rectBuf[:size]
	if fw, fh := src.FramebufferWriteRectTopDown(f.rectBuf, rect.x, rect.y, rect.w, rect.h); fw != w || fh != h {
		return Event{}, false
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
		Data:       base64.StdEncoding.EncodeToString(f.rectBuf),
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
