package opskat

import (
	"encoding/json"
	"fmt"
	"io"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

func TestIOHandle(t *testing.T) {
	Convey("IOHandle", t, func() {
		stub := newIOStub()
		SetHostStub(stub)
		defer SetHostStub(nil)

		Convey("Open and Read file handle", func() {
			stub.addHandle(1, "hello world", IOMeta{Size: 11})

			h, err := IOOpen("file", map[string]any{"path": "/tmp/test.txt", "mode": "read"})
			So(err, ShouldBeNil)
			So(h.ID(), ShouldEqual, 1)
			So(h.Meta().Size, ShouldEqual, 11)

			data, err := io.ReadAll(h)
			So(err, ShouldBeNil)
			So(string(data), ShouldEqual, "hello world")

			err = h.Close()
			So(err, ShouldBeNil)
		})

		Convey("Open and Write file handle", func() {
			stub.addHandle(2, "", IOMeta{})

			h, err := IOOpen("file", map[string]any{"path": "/tmp/out.txt", "mode": "write"})
			So(err, ShouldBeNil)

			n, err := h.Write([]byte("written data"))
			So(err, ShouldBeNil)
			So(n, ShouldEqual, 12)

			So(string(stub.written[2]), ShouldEqual, "written data")
		})
	})
}

// ioStub implements hostCaller with in-memory IO handles.
type ioStub struct {
	nopHost
	handles map[uint32]*ioStubEntry
	written map[uint32][]byte
	nextID  uint32
}

type ioStubEntry struct {
	data []byte
	meta IOMeta
	pos  int
}

func newIOStub() *ioStub {
	return &ioStub{
		handles: make(map[uint32]*ioStubEntry),
		written: make(map[uint32][]byte),
		nextID:  1,
	}
}

func (s *ioStub) addHandle(id uint32, data string, meta IOMeta) {
	s.handles[id] = &ioStubEntry{data: []byte(data), meta: meta}
	s.nextID = id
}

func (s *ioStub) IOOpen(params []byte) (uint32, []byte, error) {
	id := s.nextID
	s.nextID++
	entry, ok := s.handles[id]
	if !ok {
		entry = &ioStubEntry{}
		s.handles[id] = entry
	}
	metaJSON, _ := json.Marshal(entry.meta)
	return id, metaJSON, nil
}

func (s *ioStub) IORead(handleID uint32, size int) ([]byte, error) {
	entry, ok := s.handles[handleID]
	if !ok {
		return nil, fmt.Errorf("handle not found")
	}
	if entry.pos >= len(entry.data) {
		return nil, io.EOF
	}
	end := entry.pos + size
	if end > len(entry.data) {
		end = len(entry.data)
	}
	data := entry.data[entry.pos:end]
	entry.pos = end
	return data, nil
}

func (s *ioStub) IOWrite(handleID uint32, data []byte) (int, error) {
	s.written[handleID] = append(s.written[handleID], data...)
	return len(data), nil
}

func (s *ioStub) IOFlush(handleID uint32) ([]byte, error) {
	entry, ok := s.handles[handleID]
	if !ok {
		return nil, fmt.Errorf("handle not found")
	}
	metaJSON, _ := json.Marshal(entry.meta)
	return metaJSON, nil
}

func (s *ioStub) IOClose(handleID uint32) error {
	delete(s.handles, handleID)
	return nil
}
