package ssh_svc

import (
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// blockingStdin blocks Write until Close is called, like a stalled SSH channel.
type blockingStdin struct {
	entered chan struct{}
	release chan struct{}
}

func (b *blockingStdin) Write(p []byte) (int, error) {
	b.entered <- struct{}{}
	<-b.release
	return 0, io.ErrClosedPipe
}

func (b *blockingStdin) Close() error {
	select {
	case <-b.release:
	default:
		close(b.release)
	}
	return nil
}

func withDeadline(t *testing.T, name string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("%s blocked while a Write is in flight", name)
	}
}

func TestSession_BlockedWriteDoesNotBlockCloseOrQueries(t *testing.T) {
	stdin := &blockingStdin{entered: make(chan struct{}, 2), release: make(chan struct{})}
	sess := &Session{ID: "s", shared: &sharedClient{refCount: 2}, stdin: stdin}
	t.Cleanup(func() { _ = stdin.Close() })

	writeDone := make(chan error, 1)
	go func() { writeDone <- sess.Write([]byte("x")) }()
	<-stdin.entered

	withDeadline(t, "IsClosed", func() { assert.False(t, sess.IsClosed()) })
	withDeadline(t, "Close", func() { sess.Close() })
	assert.True(t, sess.IsClosed())
	withDeadline(t, "Resize", func() { assert.EqualError(t, sess.Resize(80, 24), "session is closed") })

	// Closing the underlying channel is what releases the stalled writer.
	_ = stdin.Close()
	select {
	case err := <-writeDone:
		assert.Error(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("blocked Write was not released")
	}
	assert.EqualError(t, sess.Write([]byte("y")), "session is closed")
}
