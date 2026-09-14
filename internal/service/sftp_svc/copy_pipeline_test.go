package sftp_svc

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/pkg/sftp"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/pkg/sftpio/sftpiotest"
)

// 远端到远端的粘贴曾经用 io.Copy：它选中源的 WriteTo，读腿确实并发，但随后每次只把一个
// maxPacket 的块交给目的端 Write —— 而不超过一个包的写入永远是单次往返，写腿整条退化成
// 串行，连客户端的并发写选项都绕不过去。两条腿都必须自己流水线。
func TestCopyRemoteFilePipelinesBothLegs(t *testing.T) {
	body := bytes.Repeat([]byte("opskat"), 8*32*1024/6)

	// 每次只给被观测的那条腿加往返延迟，另一条腿全速，免得它成为瓶颈把结论掩盖掉。
	t.Run("读腿", func(t *testing.T) {
		srcRoot, dstRoot := seedCopyRoots(t, body)
		srcClient, observed := sftpiotest.NewClient(t, srcRoot, sftpiotest.PacketRead)
		runRemoteCopy(t, srcClient, sftpiotest.NewPlainClient(t, dstRoot), srcRoot, dstRoot, body)
		require.GreaterOrEqualf(t, observed.MaxInFlight(), 4,
			"同时在途的读请求 = %d：复制的读腿退化成了逐包往返", observed.MaxInFlight())
	})

	t.Run("写腿", func(t *testing.T) {
		srcRoot, dstRoot := seedCopyRoots(t, body)
		dstClient, observed := sftpiotest.NewClient(t, dstRoot, sftpiotest.PacketWrite)
		runRemoteCopy(t, sftpiotest.NewPlainClient(t, srcRoot), dstClient, srcRoot, dstRoot, body)
		require.GreaterOrEqualf(t, observed.MaxInFlight(), 4,
			"同时在途的写请求 = %d：复制的写腿退化成了逐包往返", observed.MaxInFlight())
	})
}

func seedCopyRoots(t *testing.T, body []byte) (srcRoot, dstRoot string) {
	t.Helper()
	srcRoot, dstRoot = t.TempDir(), t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(srcRoot, "app.bin"), body, 0o600))
	return srcRoot, dstRoot
}

func runRemoteCopy(t *testing.T, src, dst *sftp.Client, srcRoot, dstRoot string, body []byte) {
	t.Helper()
	err := copyRemoteFile(context.Background(),
		sftpCopyClient{client: src}, sftpCopyClient{client: dst},
		filepath.Join(srcRoot, "app.bin"), filepath.Join(dstRoot, "app.bin"))
	require.NoError(t, err)

	got, err := os.ReadFile(filepath.Join(dstRoot, "app.bin")) //nolint:gosec // 测试自己建的临时目录
	require.NoError(t, err)
	require.Equal(t, body, got)
}
