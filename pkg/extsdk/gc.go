package opskat

import (
	"runtime"
	"runtime/metrics"
)

// callGarbageLimit is how far the heap may grow past what the last collection at
// a call boundary left alive before the next call boundary collects again.
//
// A guest instance lives in a fixed linear memory (the host caps it at 64 MiB,
// the module's static data included) and is idle between calls, so Go's pacer
// never gets to run: after a call that held a lot at once, its next goal is
// twice that, and the dead values stay in the heap until it is reached. A large
// allocation that finds no free span grows linear memory instead of collecting
// first, so a few such calls in a row run the instance out of memory. 8 MiB of
// garbage is far below that budget, and freeing it costs one collection per
// 8 MiB a call allocated and dropped.
const callGarbageLimit = 8 << 20

// heapObjectsMetric is the bytes of heap objects, live or not yet swept —
// MemStats.HeapAlloc, read without stopping the world.
const heapObjectsMetric = "/memory/classes/heap/objects:bytes"

// callHeap collects garbage at the end of a guest call when the heap has grown
// more than callGarbageLimit past what the last such collection left alive.
// Measuring from that baseline rather than from zero keeps an extension that
// legitimately holds more than the limit across calls from collecting on every
// call.
type callHeap struct {
	sample   []metrics.Sample
	retained uint64
}

func newCallHeap() *callHeap {
	return &callHeap{sample: []metrics.Sample{{Name: heapObjectsMetric}}}
}

// afterCall runs once a call's reply is packed, when everything the call
// allocated but the reply is garbage.
func (h *callHeap) afterCall() {
	if h.heapObjects() <= h.retained+callGarbageLimit {
		return
	}
	runtime.GC()
	h.retained = h.heapObjects()
}

func (h *callHeap) heapObjects() uint64 {
	metrics.Read(h.sample)
	return h.sample[0].Value.Uint64()
}
