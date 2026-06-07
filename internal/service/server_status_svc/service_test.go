package server_status_svc

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"io"
	"net"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestParseSnapshot(t *testing.T) {
	raw := `OS=Linux
HOST=prod-web-01
UPTIME=10:17:42 up 12 days, 3:11, 1 user, load average: 0.32, 0.28, 0.25
LOAD1=0.32
LOAD5=0.28
LOAD15=0.25
CPU_PERCENT=18.6
MEM_TOTAL_BYTES=8589934592
MEM_USED_BYTES=4294967296
DISK_MOUNT=/
DISK_TOTAL_BYTES=21474836480
DISK_USED_BYTES=6442450944
`

	snapshot, err := parseSnapshot(raw)
	if err != nil {
		t.Fatalf("parseSnapshot returned error: %v", err)
	}
	if snapshot.Hostname != "prod-web-01" {
		t.Fatalf("Hostname = %q, want prod-web-01", snapshot.Hostname)
	}
	if snapshot.OS != "Linux" {
		t.Fatalf("OS = %q, want Linux", snapshot.OS)
	}
	if snapshot.CPUPercent != 18.6 {
		t.Fatalf("CPUPercent = %v, want 18.6", snapshot.CPUPercent)
	}
	if snapshot.MemoryTotalBytes != 8589934592 {
		t.Fatalf("MemoryTotalBytes = %d, want 8589934592", snapshot.MemoryTotalBytes)
	}
	if snapshot.MemoryUsedBytes != 4294967296 {
		t.Fatalf("MemoryUsedBytes = %d, want 4294967296", snapshot.MemoryUsedBytes)
	}
	if snapshot.DiskMount != "/" {
		t.Fatalf("DiskMount = %q, want /", snapshot.DiskMount)
	}
	if snapshot.DiskUsedBytes != 6442450944 {
		t.Fatalf("DiskUsedBytes = %d, want 6442450944", snapshot.DiskUsedBytes)
	}
}

func TestParseSnapshotRejectsEmptyPayload(t *testing.T) {
	if _, err := parseSnapshot(""); err == nil {
		t.Fatal("expected parseSnapshot to reject empty payload")
	}
}

func TestCollectRunsSnapshotCommandOverSSH(t *testing.T) {
	client := newTestSSHClient(t, func(cmd string) (string, string, uint32) {
		if cmd != snapshotCommand {
			return "", "unexpected command", 1
		}
		return `OS=Linux
HOST=test-host
UPTIME=up 1 day
LOAD1=0.10
LOAD5=0.20
LOAD15=0.30
CPU_PERCENT=12.5
MEM_TOTAL_BYTES=1024
MEM_USED_BYTES=512
DISK_MOUNT=/
DISK_TOTAL_BYTES=2048
DISK_USED_BYTES=1024
`, "", 0
	})
	defer func() {
		_ = client.Close()
	}()

	snapshot, err := Collect(context.Background(), client)
	if err != nil {
		t.Fatalf("Collect returned error: %v", err)
	}
	if snapshot.Hostname != "test-host" {
		t.Fatalf("Hostname = %q, want test-host", snapshot.Hostname)
	}
	if snapshot.CPUPercent != 12.5 {
		t.Fatalf("CPUPercent = %v, want 12.5", snapshot.CPUPercent)
	}
	if snapshot.CollectedAt == 0 {
		t.Fatal("CollectedAt was not set")
	}
}

func TestCollectReturnsRemoteStderr(t *testing.T) {
	client := newTestSSHClient(t, func(string) (string, string, uint32) {
		return "", "permission denied", 1
	})
	defer func() {
		_ = client.Close()
	}()

	_, err := Collect(context.Background(), client)
	if err == nil {
		t.Fatal("expected Collect to fail")
	}
	if err.Error() != "collect server status failed: permission denied" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func newTestSSHClient(t *testing.T, onExec func(cmd string) (stdout string, stderr string, exit uint32)) *ssh.Client {
	t.Helper()

	signer := newTestSigner(t)
	serverConfig := &ssh.ServerConfig{NoClientAuth: true}
	serverConfig.AddHostKey(signer)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	go func() {
		defer func() {
			_ = listener.Close()
		}()
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer func() {
			_ = conn.Close()
		}()

		serverConn, chans, reqs, err := ssh.NewServerConn(conn, serverConfig)
		if err != nil {
			return
		}
		defer func() {
			_ = serverConn.Close()
		}()
		go ssh.DiscardRequests(reqs)

		for newChannel := range chans {
			if newChannel.ChannelType() != "session" {
				_ = newChannel.Reject(ssh.UnknownChannelType, "unsupported channel type")
				continue
			}
			channel, requests, err := newChannel.Accept()
			if err != nil {
				continue
			}
			go handleTestSession(channel, requests, onExec)
		}
	}()

	clientConfig := &ssh.ClientConfig{
		User:            "tester",
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}
	conn, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatalf("dial tcp: %v", err)
	}
	clientConn, chans, reqs, err := ssh.NewClientConn(conn, listener.Addr().String(), clientConfig)
	if err != nil {
		t.Fatalf("dial ssh: %v", err)
	}
	return ssh.NewClient(clientConn, chans, reqs)
}

func handleTestSession(channel ssh.Channel, requests <-chan *ssh.Request, onExec func(cmd string) (string, string, uint32)) {
	defer func() {
		_ = channel.Close()
	}()

	for req := range requests {
		switch req.Type {
		case "exec":
			var payload struct {
				Command string
			}
			if err := ssh.Unmarshal(req.Payload, &payload); err != nil {
				_ = req.Reply(false, nil)
				return
			}
			stdout, stderr, exitCode := onExec(payload.Command)
			_ = req.Reply(true, nil)
			if stdout != "" {
				_, _ = io.WriteString(channel, stdout)
			}
			if stderr != "" {
				_, _ = channel.Stderr().Write([]byte(stderr))
			}
			_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{Status: exitCode}))
			return
		default:
			_ = req.Reply(false, nil)
		}
	}
}

func newTestSigner(t *testing.T) ssh.Signer {
	t.Helper()
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}
	return signer
}
