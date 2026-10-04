package opskat

import (
	"encoding/json"
	"fmt"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

func TestHostCallStub(t *testing.T) {
	Convey("hostcall stub", t, func() {
		Convey("stub is pluggable", func() {
			// Default stub should not panic
			hostLog("info", "test message")

			called := false
			SetHostStub(&mockHostCaller{
				logFn: func(level, msg string) { called = true },
			})
			defer SetHostStub(nil)

			hostLog("info", "hello")
			So(called, ShouldBeTrue)
		})

		Convey("hostIOOpen returns error by default", func() {
			SetHostStub(nil)
			_, _, err := hostIOOpen([]byte(`{"type":"file","path":"/tmp/x","mode":"read"}`))
			So(err, ShouldNotBeNil)
		})
	})
}

// mockHostCaller is a test helper
type mockHostCaller struct {
	logFn    func(level, msg string)
	ioOpenFn func(params []byte) (uint32, []byte, error)
}

func (m *mockHostCaller) Log(level, msg string) {
	if m.logFn != nil {
		m.logFn(level, msg)
	}
}

func (m *mockHostCaller) IOOpen(params []byte) (uint32, []byte, error) {
	if m.ioOpenFn != nil {
		return m.ioOpenFn(params)
	}
	return 0, nil, fmt.Errorf("not implemented")
}

func (m *mockHostCaller) IORead(handleID uint32, size int) ([]byte, error) {
	return nil, fmt.Errorf("not implemented")
}

func (m *mockHostCaller) IOWrite(handleID uint32, data []byte) (int, error) {
	return 0, fmt.Errorf("not implemented")
}

func (m *mockHostCaller) IOFlush(handleID uint32) ([]byte, error) {
	return nil, fmt.Errorf("not implemented")
}

func (m *mockHostCaller) IOClose(handleID uint32) error {
	return fmt.Errorf("not implemented")
}

func (m *mockHostCaller) AssetGetConfig(assetID int64) (json.RawMessage, error) {
	return nil, fmt.Errorf("not implemented")
}

func (m *mockHostCaller) FileDialog(params []byte) (string, error) {
	return "", fmt.Errorf("not implemented")
}

func (m *mockHostCaller) KVGet(key string) ([]byte, error) {
	return nil, fmt.Errorf("not implemented")
}

func (m *mockHostCaller) KVSet(key string, value []byte) error {
	return fmt.Errorf("not implemented")
}

func (m *mockHostCaller) ActionEvent(eventType string, data []byte) {
}

func (m *mockHostCaller) IOSetDeadline(handleID uint32, kind string, unixNanos int64) error {
	return fmt.Errorf("not implemented")
}

func (m *mockHostCaller) ActionShouldStop() bool {
	return false
}
