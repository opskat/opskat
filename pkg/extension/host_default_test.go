// pkg/extension/host_default_test.go
package extension

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/opskat/opskat/internal/pkg/proxychain"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/zap"
)

func TestDefaultHostProvider(t *testing.T) {
	Convey("DefaultHostProvider", t, func() {
		logger, _ := zap.NewDevelopment()
		host := NewDefaultHostProvider(DefaultHostConfig{
			Logger: logger,
		})
		hs := newHostSession(host)
		defer hs.CloseAll()

		Convey("IOOpen file read", func() {
			dir := t.TempDir()
			path := filepath.Join(dir, "test.txt")
			So(os.WriteFile(path, []byte("content"), 0644), ShouldBeNil)

			id, meta, err := hs.Open(IOOpenParams{Type: "file", Path: path, Mode: "read"})
			So(err, ShouldBeNil)
			So(id, ShouldBeGreaterThan, 0)
			So(meta.Size, ShouldEqual, 7)

			data, err := hs.Read(id, 100)
			So(err, ShouldBeNil)
			So(string(data), ShouldEqual, "content")

			So(hs.Close(id), ShouldBeNil)
		})

		Convey("IOOpen file write", func() {
			dir := t.TempDir()
			path := filepath.Join(dir, "out.txt")

			id, _, err := hs.Open(IOOpenParams{Type: "file", Path: path, Mode: "write"})
			So(err, ShouldBeNil)

			n, err := hs.Write(id, []byte("output"))
			So(err, ShouldBeNil)
			So(n, ShouldEqual, 6)

			So(hs.Close(id), ShouldBeNil)
			data, _ := os.ReadFile(path) //nolint:gosec // test file with known path
			So(string(data), ShouldEqual, "output")
		})

		Convey("IOOpen unknown type returns error", func() {
			_, _, err := hs.Open(IOOpenParams{Type: "unknown"})
			So(err, ShouldNotBeNil)
		})

		Convey("unconfigured services return errors", func() {
			_, err := host.GetAssetConfig(1)
			So(err, ShouldNotBeNil)

			_, err = host.KVGet("key")
			So(err, ShouldNotBeNil)
		})
	})
}

func TestDefaultHostProvider_IOReadEOF(t *testing.T) {
	Convey("IORead preserves data when underlying reader returns (n, io.EOF)", t, func() {
		// Simulate a small HTTP response where Read returns data+EOF in one call.
		// This is the exact scenario that caused "deserialization failed, failed to copy error response body, EOF".
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusForbidden)
			_, _ = fmt.Fprint(w, `<?xml version="1.0"?><Error><Code>AccessDenied</Code></Error>`)
		}))
		defer srv.Close()

		logger, _ := zap.NewDevelopment()
		host := NewDefaultHostProvider(DefaultHostConfig{Logger: logger})
		hs := newHostSession(host)
		defer hs.CloseAll()

		id, _, err := hs.Open(IOOpenParams{
			Type:         "http",
			Method:       "GET",
			URL:          srv.URL,
			AllowPrivate: true, // httptest server binds to loopback
		})
		So(err, ShouldBeNil)

		meta, err := hs.Flush(id)
		So(err, ShouldBeNil)
		So(meta.Status, ShouldEqual, 403)

		// Read entire response body through IORead
		var body []byte
		for {
			data, err := hs.Read(id, 4096)
			if len(data) > 0 {
				body = append(body, data...)
			}
			if err != nil {
				So(err, ShouldEqual, io.EOF)
				break
			}
		}

		So(string(body), ShouldContainSubstring, "AccessDenied")

		So(hs.Close(id), ShouldBeNil)
	})
}

// TestOpenTCP_AnswerWaitIsNotCountedAgainstTheDialTimeout: an asset's connection path
// can stop on an SSH hop's MFA prompt; the time a person takes to answer it is not
// counted against the TCP dial timeout, while canceling the call still ends it.
func TestOpenTCP_AnswerWaitIsNotCountedAgainstTheDialTimeout(t *testing.T) {
	Convey("Given a connection path that waits for a person's answer longer than the dial timeout", t, func() {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		So(err, ShouldBeNil)
		defer func() { _ = ln.Close() }()
		params := IOOpenParams{Type: "tcp", Addr: ln.Addr().String(), Timeout: 100}

		// waitingDial stands in for a path whose hop prompts: it waits inside
		// AwaitAnswer, as the hop's MFA responder does, then connects. waiting is
		// closed once the wait has begun.
		waitingDial := func(wait time.Duration, waiting chan<- struct{}) DialContextFunc {
			return func(ctx context.Context, network, addr string) (net.Conn, error) {
				resume := proxychain.AwaitAnswer(ctx)
				if waiting != nil {
					close(waiting)
				}
				select {
				case <-time.After(wait):
				case <-ctx.Done():
					resume()
					return nil, context.Cause(ctx)
				}
				resume()
				return (&net.Dialer{}).DialContext(ctx, network, addr)
			}
		}

		Convey("an answer arriving after the timeout still connects", func() {
			res, err := openTCP(context.Background(), waitingDial(400*time.Millisecond, nil), nil, params, nil)
			So(err, ShouldBeNil)
			So(res.Closer.Close(), ShouldBeNil)
		})

		Convey("canceling the call during the wait ends it", func() {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			waiting := make(chan struct{})
			go func() {
				<-waiting
				time.Sleep(300 * time.Millisecond) // past the dial timeout
				cancel()
			}()
			start := time.Now()
			_, err := openTCP(ctx, waitingDial(time.Minute, waiting), nil, params, nil)
			So(errors.Is(err, context.Canceled), ShouldBeTrue)
			So(time.Since(start), ShouldBeLessThan, 5*time.Second)
		})
	})
}
