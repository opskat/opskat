//go:build wasip1

package opskat

import "unsafe"

// pinned holds buffers handed to the host, keyed by their guest pointer.
//
// The Go GC is non-moving, so a pointer stays valid for as long as the slice is
// reachable — but nothing else references these buffers, so without this map the
// collector would reclaim them while the host still holds the pointer. Under the
// old "one instance per call" model the instance died before that mattered; a
// reactor instance is long-lived and the GC runs, so free() must be real.
var pinned = map[uint32][]byte{}

// malloc allocates guest memory for the host to write into.
//
//go:wasmexport malloc
func malloc(size uint32) uint32 {
	if size == 0 {
		return 0
	}
	buf := make([]byte, size)
	ptr := uint32(uintptr(unsafe.Pointer(&buf[0])))
	pinned[ptr] = buf
	return ptr
}

// free releases a buffer previously returned by malloc.
//
//go:wasmexport free
func free(ptr uint32) {
	delete(pinned, ptr)
}
