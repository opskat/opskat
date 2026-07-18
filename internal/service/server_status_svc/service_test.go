package server_status_svc

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"io"
	"net"
	"sync"
	"syscall"
	"testing"

	"golang.org/x/crypto/ssh"
)

const baseSnapshotOutput = `OS=Linux
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
`

const nvidiaSnapshotOutput = `__OPSKAT_NVIDIA_GPU_BEGIN__
0, GPU-aaaa, NVIDIA RTX 4090, 94, 23050, 24564, 67, 387.25, 450.00, 58, 550.54, 00000000:01:00.0
1, GPU-bbbb, NVIDIA RTX 4090, 2, 800, 24564, 35, 21.00, 450.00, N/A, 550.54, 00000000:02:00.0
__OPSKAT_NVIDIA_GPU_END__
__OPSKAT_NVIDIA_CUDA_VERSION__=12.4
__OPSKAT_NVIDIA_PROCESS_BEGIN__
GPU-aaaa, 101
GPU-aaaa, 202
GPU-aaaa, 303
__OPSKAT_NVIDIA_PROCESS_END__
`

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

func TestParseNVIDIAOutput(t *testing.T) {
	result, err := parseNVIDIAOutput(nvidiaSnapshotOutput)
	if err != nil {
		t.Fatalf("parseNVIDIAOutput returned error: %v", err)
	}
	if result.DriverVersion != "550.54" {
		t.Fatalf("DriverVersion = %q, want 550.54", result.DriverVersion)
	}
	if result.CUDAVersion != "12.4" {
		t.Fatalf("CUDAVersion = %q, want 12.4", result.CUDAVersion)
	}
	if len(result.GPUs) != 2 {
		t.Fatalf("len(GPUs) = %d, want 2", len(result.GPUs))
	}

	first := result.GPUs[0]
	if first.Index != 0 || first.Vendor != "NVIDIA" || first.Name != "NVIDIA RTX 4090" {
		t.Fatalf("unexpected first GPU identity: %+v", first)
	}
	if first.DeviceID != "GPU-aaaa" || first.PCIBusID != "00000000:01:00.0" {
		t.Fatalf("unexpected first GPU stable identity: %+v", first)
	}
	if first.DriverVersion != "550.54" || first.Runtime != "CUDA" || first.RuntimeVersion != "12.4" {
		t.Fatalf("unexpected first GPU metadata: %+v", first)
	}
	if first.UtilizationPercent == nil || *first.UtilizationPercent != 94 {
		t.Fatalf("first UtilizationPercent = %v, want 94", first.UtilizationPercent)
	}
	if first.MemoryUsedBytes == nil || *first.MemoryUsedBytes != 23050*1024*1024 {
		t.Fatalf("first MemoryUsedBytes = %v, want %d", first.MemoryUsedBytes, int64(23050*1024*1024))
	}
	if first.PowerDrawWatts == nil || *first.PowerDrawWatts != 387.25 {
		t.Fatalf("first PowerDrawWatts = %v, want 387.25", first.PowerDrawWatts)
	}
	if first.ComputeProcessCount == nil || *first.ComputeProcessCount != 3 {
		t.Fatalf("first ComputeProcessCount = %v, want 3", first.ComputeProcessCount)
	}

	second := result.GPUs[1]
	if second.FanPercent != nil {
		t.Fatalf("second FanPercent = %v, want nil for N/A", second.FanPercent)
	}
	if second.ComputeProcessCount == nil || *second.ComputeProcessCount != 0 {
		t.Fatalf("second ComputeProcessCount = %v, want 0", second.ComputeProcessCount)
	}
}

func TestParseNVIDIAOutputRejectsMalformedGPURecord(t *testing.T) {
	raw := `__OPSKAT_NVIDIA_GPU_BEGIN__
0, GPU-aaaa, NVIDIA RTX 4090
__OPSKAT_NVIDIA_GPU_END__
__OPSKAT_NVIDIA_PROCESS_BEGIN__
__OPSKAT_NVIDIA_PROCESS_END__
`
	if _, err := parseNVIDIAOutput(raw); err == nil {
		t.Fatal("expected parseNVIDIAOutput to reject a malformed GPU record")
	}
}

func TestParseNVIDIAOutputAllowsNoGPUs(t *testing.T) {
	raw := `__OPSKAT_NVIDIA_GPU_BEGIN__
__OPSKAT_NVIDIA_GPU_END__
__OPSKAT_NVIDIA_CUDA_VERSION__=12.4
__OPSKAT_NVIDIA_PROCESS_BEGIN__
__OPSKAT_NVIDIA_PROCESS_END__
`
	result, err := parseNVIDIAOutput(raw)
	if err != nil {
		t.Fatalf("parseNVIDIAOutput returned error: %v", err)
	}
	if len(result.GPUs) != 0 {
		t.Fatalf("len(GPUs) = %d, want 0", len(result.GPUs))
	}
}

func TestParseNVIDIAOutputKeepsGPUsWhenProcessQueryIsUnavailable(t *testing.T) {
	raw := `__OPSKAT_NVIDIA_GPU_BEGIN__
0, GPU-aaaa, NVIDIA RTX 4090, 94, 23050, 24564, 67, 387.25, 450.00, 58, 550.54
__OPSKAT_NVIDIA_GPU_END__
__OPSKAT_NVIDIA_CUDA_VERSION__=12.4
__OPSKAT_NVIDIA_PROCESS_BEGIN__
__OPSKAT_NVIDIA_PROCESS_UNAVAILABLE__
__OPSKAT_NVIDIA_PROCESS_END__
`
	result, err := parseNVIDIAOutput(raw)
	if err != nil {
		t.Fatalf("parseNVIDIAOutput returned error: %v", err)
	}
	if len(result.GPUs) != 1 {
		t.Fatalf("len(GPUs) = %d, want 1", len(result.GPUs))
	}
	if result.GPUs[0].ComputeProcessCount != nil {
		t.Fatalf("ComputeProcessCount = %v, want nil", result.GPUs[0].ComputeProcessCount)
	}
}

func TestCollectKeepsBaseStatusWhenNoSupportedGPUToolIsAvailable(t *testing.T) {
	var commands []string
	var commandsMu sync.Mutex
	snapshot, err := collectWithRunner(context.Background(), func(_ context.Context, cmd, _ string) (string, error) {
		commandsMu.Lock()
		commands = append(commands, cmd)
		commandsMu.Unlock()
		switch cmd {
		case snapshotCommand:
			return baseSnapshotOutput, nil
		case nvidiaSMICommand, amdSMICommand, rocmSMICommand, xpuSMICommand:
			return "", nil
		default:
			return "", errors.New("unexpected command")
		}
	})
	if err != nil {
		t.Fatalf("collectWithRunner returned error: %v", err)
	}
	commandsMu.Lock()
	assertGPUCommandSet(t, commands)
	commandsMu.Unlock()
	if snapshot.Hostname != "test-host" {
		t.Fatalf("Hostname = %q, want test-host", snapshot.Hostname)
	}
	if snapshot.CPUPercent != 12.5 {
		t.Fatalf("CPUPercent = %v, want 12.5", snapshot.CPUPercent)
	}
	if snapshot.CollectedAt == 0 {
		t.Fatal("CollectedAt was not set")
	}
	if len(snapshot.GPUs) != 0 {
		t.Fatalf("len(GPUs) = %d, want 0", len(snapshot.GPUs))
	}
}

func TestCollectRunsCommandsOverSSH(t *testing.T) {
	var commands []string
	var commandsMu sync.Mutex
	client := newTestSSHClient(t, func(cmd string) (string, string, uint32) {
		commandsMu.Lock()
		commands = append(commands, cmd)
		commandsMu.Unlock()
		switch cmd {
		case snapshotCommand:
			return baseSnapshotOutput, "", 0
		case nvidiaSMICommand, amdSMICommand, rocmSMICommand, xpuSMICommand:
			return "", "", 0
		default:
			return "", "unexpected command", 1
		}
	})
	defer func() {
		_ = client.Close()
	}()

	if _, err := Collect(context.Background(), client); err != nil {
		t.Fatalf("Collect returned error: %v", err)
	}
	commandsMu.Lock()
	defer commandsMu.Unlock()
	assertGPUCommandSet(t, commands)
}

func TestCollectAddsNVIDIAGPUs(t *testing.T) {
	snapshot, err := collectWithRunner(context.Background(), func(_ context.Context, cmd, _ string) (string, error) {
		switch cmd {
		case snapshotCommand:
			return baseSnapshotOutput, nil
		case nvidiaSMICommand:
			return nvidiaSnapshotOutput, nil
		case amdSMICommand, rocmSMICommand, xpuSMICommand:
			return "", nil
		default:
			return "", errors.New("unexpected command")
		}
	})
	if err != nil {
		t.Fatalf("collectWithRunner returned error: %v", err)
	}
	if len(snapshot.GPUs) != 2 {
		t.Fatalf("len(GPUs) = %d, want 2", len(snapshot.GPUs))
	}
	if snapshot.GPUDriverVersion != "550.54" || snapshot.CUDAVersion != "12.4" {
		t.Fatalf("unexpected GPU metadata: driver=%q cuda=%q", snapshot.GPUDriverVersion, snapshot.CUDAVersion)
	}
}

func TestCollectCombinesNVIDIAAMDAndIntelGPUs(t *testing.T) {
	amdOutput := amdSMIFixtureOutput(t)
	intelOutput := xpuSMIFixtureOutput(t)

	snapshot, err := collectWithRunner(context.Background(), func(_ context.Context, cmd, _ string) (string, error) {
		switch cmd {
		case snapshotCommand:
			return baseSnapshotOutput, nil
		case nvidiaSMICommand:
			return nvidiaSnapshotOutput, nil
		case amdSMICommand:
			return amdOutput, nil
		case xpuSMICommand:
			return intelOutput, nil
		case rocmSMICommand:
			return "", errors.New("rocm-smi fallback must not run when amd-smi is usable")
		default:
			return "", errors.New("unexpected command")
		}
	})
	if err != nil {
		t.Fatalf("collectWithRunner returned error: %v", err)
	}
	if len(snapshot.GPUs) != 4 {
		t.Fatalf("len(GPUs) = %d, want 4", len(snapshot.GPUs))
	}
	wantVendors := []string{"NVIDIA", "NVIDIA", "AMD", "Intel"}
	for i, want := range wantVendors {
		if snapshot.GPUs[i].Vendor != want {
			t.Fatalf("GPU vendors = %+v, want %#v", snapshot.GPUs, wantVendors)
		}
	}
}

func TestCollectPreservesAMDAndIntelWhenNVIDIAOutputIsMalformed(t *testing.T) {
	amdOutput := amdSMIFixtureOutput(t)
	intelOutput := xpuSMIFixtureOutput(t)
	snapshot, err := collectWithRunner(context.Background(), func(_ context.Context, cmd, _ string) (string, error) {
		switch cmd {
		case snapshotCommand:
			return baseSnapshotOutput, nil
		case nvidiaSMICommand:
			return nvidiaGPUBegin + "\nmalformed\n" + nvidiaGPUEnd + "\n", nil
		case amdSMICommand:
			return amdOutput, nil
		case xpuSMICommand:
			return intelOutput, nil
		case rocmSMICommand:
			return "", errors.New("unexpected AMD fallback")
		default:
			return "", errors.New("unexpected command")
		}
	})
	if err != nil {
		t.Fatalf("collectWithRunner returned error: %v", err)
	}
	if len(snapshot.GPUs) != 2 || snapshot.GPUs[0].Vendor != "AMD" || snapshot.GPUs[1].Vendor != "Intel" {
		t.Fatalf("successful vendor results were not preserved: %+v", snapshot.GPUs)
	}
}

func TestCollectKeepsBaseStatusWhenGPUCollectionDegrades(t *testing.T) {
	tests := []struct {
		name   string
		stdout string
		err    error
	}{
		{name: "command failure", err: errors.New("collect NVIDIA GPU status failed: NVIDIA-SMI has failed")},
		{
			name: "parsing failure",
			stdout: `__OPSKAT_NVIDIA_GPU_BEGIN__
malformed
__OPSKAT_NVIDIA_GPU_END__
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gpuCalls := 0
			snapshot, err := collectWithRunner(context.Background(), func(_ context.Context, cmd, _ string) (string, error) {
				switch cmd {
				case snapshotCommand:
					return baseSnapshotOutput, nil
				case nvidiaSMICommand:
					gpuCalls++
					return tt.stdout, tt.err
				case amdSMICommand, rocmSMICommand, xpuSMICommand:
					return "", nil
				default:
					return "", errors.New("unexpected command")
				}
			})
			if err != nil {
				t.Fatalf("collectWithRunner returned error: %v", err)
			}
			if gpuCalls != 1 {
				t.Fatalf("GPU command calls = %d, want 1", gpuCalls)
			}
			if snapshot.Hostname != "test-host" || snapshot.CPUPercent != 12.5 {
				t.Fatalf("base snapshot was not preserved: %+v", snapshot)
			}
			if len(snapshot.GPUs) != 0 {
				t.Fatalf("len(GPUs) = %d, want 0", len(snapshot.GPUs))
			}
		})
	}
}

func assertGPUCommandSet(t *testing.T, commands []string) {
	t.Helper()
	if len(commands) != 5 || commands[0] != snapshotCommand {
		t.Fatalf("commands = %#v, want base snapshot plus four optional GPU probes", commands)
	}
	seen := make(map[string]int)
	for _, command := range commands[1:] {
		seen[command]++
	}
	for _, command := range []string{nvidiaSMICommand, amdSMICommand, rocmSMICommand, xpuSMICommand} {
		if seen[command] != 1 {
			t.Fatalf("GPU command count for %q = %d, want 1; commands = %#v", command, seen[command], commands)
		}
	}
}

func TestCollectReturnsBaseCommandError(t *testing.T) {
	_, err := collectWithRunner(context.Background(), func(_ context.Context, cmd, _ string) (string, error) {
		if cmd != snapshotCommand {
			return "", errors.New("unexpected command")
		}
		return "", errors.New("collect server status failed: permission denied")
	})
	if err == nil {
		t.Fatal("expected Collect to fail")
	}
	if err.Error() != "collect server status failed: permission denied" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func newTestSSHClient(t *testing.T, onExec func(cmd string) (stdout string, stderr string, exit uint32)) *ssh.Client {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		if errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES) {
			t.Skipf("loopback listener unavailable: %v", err)
		}
		t.Fatalf("listen: %v", err)
	}

	signer := newTestSigner(t)
	serverConfig := &ssh.ServerConfig{NoClientAuth: true}
	serverConfig.AddHostKey(signer)

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
