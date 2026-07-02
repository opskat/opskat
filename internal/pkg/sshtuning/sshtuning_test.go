package sshtuning

import (
	"net"
	"testing"
	"time"
)

func TestDefault(t *testing.T) {
	d := Default()
	if !d.TCPNoDelay {
		t.Errorf("TCPNoDelay should default to true")
	}
	if !d.TCPKeepAlive {
		t.Errorf("TCPKeepAlive should default to true")
	}
	if d.KeepAliveInterval != DefaultKeepAliveInterval {
		t.Errorf("KeepAliveInterval = %v, want %v", d.KeepAliveInterval, DefaultKeepAliveInterval)
	}
	if d.DialTimeout != DefaultDialTimeout {
		t.Errorf("DialTimeout = %v, want %v", d.DialTimeout, DefaultDialTimeout)
	}
}

func TestGetSet(t *testing.T) {
	// Restore the global after the test so other packages' tests see defaults.
	orig := Get()
	t.Cleanup(func() { Set(orig) })

	want := Settings{
		TCPNoDelay:        false,
		TCPKeepAlive:      false,
		KeepAliveInterval: 15 * time.Second,
		DialTimeout:       5 * time.Second,
	}
	Set(want)
	if got := Get(); got != want {
		t.Fatalf("Get() = %+v, want %+v", got, want)
	}
}

func TestDialTimeoutOrDefault(t *testing.T) {
	if got := (Settings{}).DialTimeoutOrDefault(); got != DefaultDialTimeout {
		t.Errorf("zero DialTimeout = %v, want default %v", got, DefaultDialTimeout)
	}
	if got := (Settings{DialTimeout: -1}).DialTimeoutOrDefault(); got != DefaultDialTimeout {
		t.Errorf("negative DialTimeout = %v, want default %v", got, DefaultDialTimeout)
	}
	if got := (Settings{DialTimeout: 7 * time.Second}).DialTimeoutOrDefault(); got != 7*time.Second {
		t.Errorf("DialTimeout = %v, want 7s", got)
	}
}

func TestResolveKeepAlive(t *testing.T) {
	// Restore the global default after the test.
	orig := Get()
	t.Cleanup(func() { Set(orig) })
	Set(Settings{KeepAliveInterval: 30 * time.Second})

	t.Run("positive override wins", func(t *testing.T) {
		if got := ResolveKeepAlive(45); got != 45*time.Second {
			t.Errorf("ResolveKeepAlive(45) = %v, want 45s", got)
		}
	})
	t.Run("zero falls back to global default", func(t *testing.T) {
		if got := ResolveKeepAlive(0); got != 30*time.Second {
			t.Errorf("ResolveKeepAlive(0) = %v, want global default 30s", got)
		}
	})
	t.Run("negative falls back to global default", func(t *testing.T) {
		if got := ResolveKeepAlive(-5); got != 30*time.Second {
			t.Errorf("ResolveKeepAlive(-5) = %v, want global default 30s", got)
		}
	})
}

func TestDialer(t *testing.T) {
	t.Run("keepalive enabled", func(t *testing.T) {
		d := Settings{TCPKeepAlive: true, DialTimeout: 8 * time.Second}.Dialer()
		if d.Timeout != 8*time.Second {
			t.Errorf("Timeout = %v, want 8s", d.Timeout)
		}
		if d.KeepAlive != 0 {
			t.Errorf("KeepAlive = %v, want 0 (default-enabled)", d.KeepAlive)
		}
	})
	t.Run("keepalive disabled", func(t *testing.T) {
		d := Settings{TCPKeepAlive: false}.Dialer()
		if d.KeepAlive >= 0 {
			t.Errorf("KeepAlive = %v, want negative (disabled)", d.KeepAlive)
		}
		if d.Timeout != DefaultDialTimeout {
			t.Errorf("Timeout = %v, want default %v", d.Timeout, DefaultDialTimeout)
		}
	})
}

func TestApplyTCPOptions(t *testing.T) {
	t.Run("non-tcp conn is a no-op", func(t *testing.T) {
		c1, c2 := net.Pipe()
		t.Cleanup(func() { _ = c1.Close(); _ = c2.Close() })
		if err := (Settings{TCPNoDelay: true}).ApplyTCPOptions(c1); err != nil {
			t.Fatalf("ApplyTCPOptions on net.Pipe conn: %v", err)
		}
	})

	t.Run("tcp conn accepts the option", func(t *testing.T) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("Listen: %v", err)
		}
		t.Cleanup(func() { _ = ln.Close() })

		conn, err := net.Dial("tcp", ln.Addr().String())
		if err != nil {
			t.Fatalf("Dial: %v", err)
		}
		t.Cleanup(func() { _ = conn.Close() })

		if err := (Settings{TCPNoDelay: false}).ApplyTCPOptions(conn); err != nil {
			t.Fatalf("ApplyTCPOptions(false): %v", err)
		}
		if err := (Settings{TCPNoDelay: true}).ApplyTCPOptions(conn); err != nil {
			t.Fatalf("ApplyTCPOptions(true): %v", err)
		}
	})
}
