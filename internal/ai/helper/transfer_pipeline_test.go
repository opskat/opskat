package helper

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/pkg/sftp"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/pkg/sftpio/sftpiotest"
)

// pipelineWant 是被断言的"同时在途请求数"。一问一答的客户端永远只能到 1，
// 取 4 是为了把"偶然重叠了两个请求"也排除在通过之外。
const pipelineWant = 4

// pipelineBody 造一个足够切成多个 32KB 请求的载荷：低于一个包就没有流水线可言。
func pipelineBody() []byte {
	return bytes.Repeat([]byte("opskat"), 8*32*1024/6)
}

func newGatedCache(t *testing.T, root string, packetType byte) (context.Context, *sftpiotest.Conn) {
	t.Helper()
	client, gate := sftpiotest.NewClient(t, root, packetType)
	cache := newSFTPClientCache(func(context.Context, int64) (*sftp.Client, io.Closer, error) {
		return client, &fakeCloser{}, nil
	})
	t.Cleanup(func() { _ = cache.Close() })
	return WithSFTPClientCache(context.Background(), cache), gate
}

// 上传（本地 -> 远端）必须把写请求流水线发出去。退化成"发一个包等一个回包"时，
// 高延迟链路上的吞吐会掉到 32KB/RTT —— 这正是 opsctl cp 比 GUI 慢一个数量级的原因。
func TestSSHTransferWritePipelinesRequests(t *testing.T) {
	root := t.TempDir()
	ctx, gate := newGatedCache(t, root, sftpiotest.PacketWrite)
	body := pipelineBody()

	err := sshTransfer.Write(ctx, &asset_entity.Asset{ID: 1}, filepath.Join(root, "app.bin"),
		bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	if got := gate.MaxInFlight(); got < pipelineWant {
		t.Fatalf("同时在途的写请求 = %d, want >= %d：上传退化成了逐包往返", got, pipelineWant)
	}
	got, err := os.ReadFile(filepath.Join(root, "app.bin")) //nolint:gosec // 测试自己建的临时目录
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("落盘内容与源不一致：%d bytes, want %d", len(got), len(body))
	}
}

// 下载（远端 -> 本地）同理：cp 的读取端被层层包装（连接绑定、字节计数）后，
// 一旦包装层挡住 *sftp.File 的 WriteTo，io.Copy 就只能逐 32KB 往返地读。
func TestSSHTransferOpenReadPipelinesRequests(t *testing.T) {
	root := t.TempDir()
	body := pipelineBody()
	if err := os.WriteFile(filepath.Join(root, "app.bin"), body, 0o600); err != nil {
		t.Fatalf("seed remote file: %v", err)
	}
	ctx, gate := newGatedCache(t, root, sftpiotest.PacketRead)

	rc, size, err := sshTransfer.OpenRead(ctx, &asset_entity.Asset{ID: 1}, filepath.Join(root, "app.bin"))
	if err != nil {
		t.Fatalf("OpenRead: %v", err)
	}
	defer func() { _ = rc.Close() }()
	if size != int64(len(body)) {
		t.Fatalf("size = %d, want %d", size, len(body))
	}

	dst := t.TempDir()
	if err := localTransfer.Write(ctx, nil, filepath.Join(dst, "app.bin"), rc, size); err != nil {
		t.Fatalf("local Write: %v", err)
	}

	if got := gate.MaxInFlight(); got < pipelineWant {
		t.Fatalf("同时在途的读请求 = %d, want >= %d：下载退化成了逐包往返", got, pipelineWant)
	}
	got, err := os.ReadFile(filepath.Join(dst, "app.bin")) //nolint:gosec // 测试自己建的临时目录
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("落盘内容与源不一致：%d bytes, want %d", len(got), len(body))
	}
}
