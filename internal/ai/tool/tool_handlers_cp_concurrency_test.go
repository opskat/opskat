package tool

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"go.uber.org/mock/gomock"
	"golang.org/x/crypto/ssh"

	"github.com/opskat/opskat/internal/ai/helper"
	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/pkg/sftpio/sftpiotest"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/asset_repo/mock_asset_repo"
)

// SFTP 请求类型（draft-ietf-secsh-filexfer-02 §3），服务端按它们给目的端的往返记账。
const (
	sftpOpen  byte = 3
	sftpClose byte = 4
	sftpLstat byte = 7
	sftpMkdir byte = 14
	sftpStat  byte = 17
)

// sftpRequestLog 记下目的端 SFTP 服务端收到的请求：按类型计数、MKDIR 按路径计数，
// 以及同时打开的文件句柄峰值（OPEN 减 CLOSE）——cp 的目的端只写文件、不开目录，
// 句柄峰值因此就是同时在写的文件数。
type sftpRequestLog struct {
	mu      sync.Mutex
	buf     []byte
	byType  map[byte]int
	mkdirs  map[string]int
	open    int
	maxOpen int
}

func newSFTPRequestLog() *sftpRequestLog {
	return &sftpRequestLog{byType: map[byte]int{}, mkdirs: map[string]int{}}
}

func (l *sftpRequestLog) feed(p []byte) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.buf = append(l.buf, p...)
	for len(l.buf) >= 4 {
		size := int(binary.BigEndian.Uint32(l.buf))
		if len(l.buf) < 4+size {
			return
		}
		l.record(l.buf[4 : 4+size])
		l.buf = l.buf[4+size:]
	}
}

func (l *sftpRequestLog) record(pkt []byte) {
	if len(pkt) == 0 {
		return
	}
	typ := pkt[0]
	l.byType[typ]++
	switch typ {
	case sftpOpen:
		l.open++
		l.maxOpen = max(l.maxOpen, l.open)
	case sftpClose:
		l.open--
	case sftpMkdir:
		// type(1) + id(4) + path(string)
		n := int(binary.BigEndian.Uint32(pkt[5:9]))
		l.mkdirs[string(pkt[9:9+n])]++
	}
}

func (l *sftpRequestLog) peakOpen() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.maxOpen
}

func (l *sftpRequestLog) mkdirCounts() map[string]int {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make(map[string]int, len(l.mkdirs))
	for k, v := range l.mkdirs {
		out[k] = v
	}
	return out
}

func (l *sftpRequestLog) count(typ byte) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.byType[typ]
}

// countingChannel 让 SFTP 服务端读到的每个字节先过一遍 sftpRequestLog。
type countingChannel struct {
	ssh.Channel
	log *sftpRequestLog
}

func (c countingChannel) Read(p []byte) (int, error) {
	n, err := c.Channel.Read(p)
	if n > 0 {
		c.log.feed(p[:n])
	}
	return n, err
}

// delayConn 是装在客户端连接读端的延迟线：每段从服务端来的字节都晚 latency 才交给客户端，
// 但不同回包的等待互相重叠——这才是一条高延迟链路的样子，并发的请求能把等待叠起来。
// （sftpiotest.Conn 每次 Read 前各睡一段，回包被串成一条队列，并发与否测不出差别。）
type delayConn struct {
	net.Conn
	latency time.Duration
	chunks  chan delayedChunk
	pending []byte
	readErr error
}

type delayedChunk struct {
	data []byte
	at   time.Time
	err  error
}

func newDelayConn(c net.Conn, latency time.Duration) *delayConn {
	d := &delayConn{Conn: c, latency: latency, chunks: make(chan delayedChunk, 4096)}
	go func() {
		for {
			buf := make([]byte, 32*1024)
			n, err := c.Read(buf)
			d.chunks <- delayedChunk{data: buf[:n], at: time.Now(), err: err}
			if err != nil {
				close(d.chunks)
				return
			}
		}
	}()
	return d
}

func (d *delayConn) Read(p []byte) (int, error) {
	for len(d.pending) == 0 {
		if d.readErr != nil {
			return 0, d.readErr
		}
		chunk, ok := <-d.chunks
		if !ok {
			return 0, io.EOF
		}
		time.Sleep(time.Until(chunk.at.Add(d.latency)))
		d.pending, d.readErr = chunk.data, chunk.err
	}
	n := copy(p, d.pending)
	d.pending = d.pending[n:]
	return n, nil
}

// startLatencySSH 起一个进程内 SSH 服务端（sftp 子系统服务真实文件系统），返回经延迟线连上
// 它的客户端，以及目的端 SFTP 请求的记账。生产代码拿到的就是一条普通的 *ssh.Client。
func startLatencySSH(t *testing.T, latency time.Duration) (*ssh.Client, *sftpRequestLog) {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate host key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatalf("host signer: %v", err)
	}
	cfg := &ssh.ServerConfig{NoClientAuth: true}
	cfg.AddHostKey(signer)

	log := newSFTPRequestLog()
	clientConn, serverConn := net.Pipe()
	var servers sync.WaitGroup
	go func() {
		_, chans, reqs, err := ssh.NewServerConn(serverConn, cfg)
		if err != nil {
			return
		}
		go ssh.DiscardRequests(reqs)
		for newCh := range chans {
			if newCh.ChannelType() != "session" {
				_ = newCh.Reject(ssh.UnknownChannelType, "session only")
				continue
			}
			ch, chReqs, err := newCh.Accept()
			if err != nil {
				return
			}
			go func() {
				for req := range chReqs {
					if req.Type != "subsystem" || !strings.HasSuffix(string(req.Payload), "sftp") {
						_ = req.Reply(false, nil)
						continue
					}
					_ = req.Reply(true, nil)
					server, err := sftp.NewServer(countingChannel{Channel: ch, log: log})
					if err != nil {
						return
					}
					servers.Add(1)
					go func() {
						defer servers.Done()
						_ = server.Serve()
						_ = server.Close()
					}()
				}
			}()
		}
	}()

	conn, chans, reqs, err := ssh.NewClientConn(newDelayConn(clientConn, latency), "pipe", &ssh.ClientConfig{
		User:            "test",
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	})
	if err != nil {
		t.Fatalf("ssh client handshake: %v", err)
	}
	client := ssh.NewClient(conn, chans, reqs)
	t.Cleanup(func() {
		_ = client.Close()
		_ = serverConn.Close()
		servers.Wait()
	})
	return client, log
}

// cpConcAssets 注册本文件用到的资产：一台真 SSH（经延迟线连到进程内服务端）与一个可控的内存端点。
func cpConcAssets(t *testing.T) {
	t.Helper()
	ctrl := gomock.NewController(t)
	m := mock_asset_repo.NewMockAssetRepo(ctrl)
	for _, a := range []*asset_entity.Asset{
		{ID: 21, Name: "lag-01", Type: asset_entity.AssetTypeSSH},
		{ID: 22, Name: "gate-01", Type: cpGateAssetType},
	} {
		m.EXPECT().Find(gomock.Any(), a.ID).Return(a, nil).AnyTimes()
		m.EXPECT().FindByName(gomock.Any(), a.Name).Return([]*asset_entity.Asset{a}, nil).AnyTimes()
	}
	orig := asset_repo.Asset()
	asset_repo.RegisterAsset(m)
	t.Cleanup(func() {
		if orig != nil {
			asset_repo.RegisterAsset(orig)
		}
	})
}

// TestCpDirectoryToSFTPRunsEntriesConcurrentlyAndCreatesEachDirOnce 是 R22 在真 SFTP 上的
// 主体：本地目录树经 cp -r 写到一条每个回包晚 latency 的 SSH 链路上。
//   - 同时在写的文件数大于 1 且不超过 8；
//   - 每个目标目录只 MKDIR 一次，不再有 MkdirAll 的 STAT，LSTAT 只剩"范围一次 + 每个目录
//     一次 + 每个文件名一次"；
//   - 字节逐个落地，进度事件 Completed 从 1 数到 N、每条恰好一次。
func TestCpDirectoryToSFTPRunsEntriesConcurrentlyAndCreatesEachDirOnce(t *testing.T) {
	const (
		latency = 10 * time.Millisecond
		dirs    = 4
		perDir  = 12
		files   = dirs * perDir
	)
	cpConcAssets(t)
	client, log := startLatencySSH(t, latency)
	sshCache := helper.NewSSHClientCache()
	if _, _, err := sshCache.GetOrDial(21, func() (*ssh.Client, io.Closer, error) { return client, nil, nil }); err != nil {
		t.Fatalf("seed ssh cache: %v", err)
	}
	ctx := helper.WithSSHCache(permission.WithPreapproved(context.Background()), sshCache)

	src := t.TempDir()
	want := map[string]string{}
	for d := 0; d < dirs; d++ {
		for f := 0; f < perDir; f++ {
			rel := filepath.Join(fmt.Sprintf("d%d", d), "sub", fmt.Sprintf("f%02d.txt", f))
			writeCpFile(t, filepath.Join(src, rel), rel)
			want[filepath.ToSlash(rel)] = rel
		}
	}
	dst := t.TempDir()
	var progress []CpProgress // 故意不加锁：进度回调必须被串行调用，-race 下才不报
	ctx = WithCpProgressObserver(ctx, func(p CpProgress) { progress = append(progress, p) })

	started := time.Now()
	out, err := handleCp(ctx, map[string]any{
		"src": src, "dst": "lag-01:" + sftpiotest.RemotePath(dst) + "/", "recursive": true,
	})
	elapsed := time.Since(started)
	if err != nil {
		t.Fatalf("cp: %v", err)
	}
	t.Logf("%d files in %d dirs over %v latency: %v, LSTAT=%d STAT=%d MKDIR=%d OPEN=%d, max open handles=%d; %s",
		files, dirs*2, latency, elapsed, log.count(sftpLstat), log.count(sftpStat), log.count(sftpMkdir),
		log.count(sftpOpen), log.peakOpen(), out)

	for rel, content := range want {
		got, readErr := os.ReadFile(filepath.Join(dst, filepath.FromSlash(rel))) //nolint:gosec // 测试临时目录
		if readErr != nil || string(got) != content {
			t.Fatalf("%s = %q, %v; want %q", rel, got, readErr, content)
		}
	}

	if peak := log.peakOpen(); peak < 2 || peak > cpTransferWorkers {
		t.Errorf("max files open at once = %d, want 2..%d (bounded concurrency)", peak, cpTransferWorkers)
	}
	mkdirs := log.mkdirCounts()
	for dir, n := range mkdirs {
		if n != 1 {
			t.Errorf("MKDIR %s sent %d times, want 1", dir, n)
		}
	}
	if len(mkdirs) != dirs*2 {
		t.Errorf("created %d directories, want %d: %v", len(mkdirs), dirs*2, mkdirs)
	}
	if n := log.count(sftpStat); n != 0 {
		t.Errorf("STAT sent %d times, want 0 (no per-file MkdirAll)", n)
	}
	if n, wantLstat := log.count(sftpLstat), 1+dirs*2+files; n != wantLstat {
		t.Errorf("LSTAT sent %d times, want %d (scope once + each dir once + each file name once)", n, wantLstat)
	}
	// 串行时每个文件至少要 OPEN、WRITE、CLOSE 三次往返；并发只要能把等待叠起来就会远低于它。
	if serialFloor := time.Duration(files*3) * latency; elapsed >= serialFloor {
		t.Errorf("elapsed %v, want below the serial floor %v", elapsed, serialFloor)
	}

	if len(progress) != files {
		t.Fatalf("progress events = %d, want %d", len(progress), files)
	}
	seen := map[string]bool{}
	for i, p := range progress {
		if p.Completed != i+1 || p.Total != files {
			t.Fatalf("progress[%d] = %d/%d, want %d/%d", i, p.Completed, p.Total, i+1, files)
		}
		seen[p.Dst] = true
	}
	if len(seen) != files {
		t.Errorf("progress named %d distinct destinations, want %d", len(seen), files)
	}
}

func writeCpFile(t *testing.T, p, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// cpGateAssetType 是一个写入可被测试拦住的内存端点：用来确定性地观察"首个错误"与"取消其余"。
const cpGateAssetType = "cpgate"

type cpGateAdapter struct {
	cpTestAdapter

	mu        sync.Mutex
	inFlight  int
	maxFlight int
	started   []string
	canceled  int
	fail      string        // 这条写入失败
	full      chan struct{} // 同时在写的条目占满并发窗口时关闭
}

var cpGate = &cpGateAdapter{}

func init() { helper.RegisterTransferAdapter(cpGateAssetType, cpGate) }

func (a *cpGateAdapter) reset(fail string) {
	a.cpTestAdapter.reset()
	a.inFlight, a.maxFlight, a.started, a.canceled = 0, 0, nil, 0
	a.fail = fail
	a.full = make(chan struct{})
}

func (a *cpGateAdapter) Write(ctx context.Context, _ *asset_entity.Asset, p string, r io.Reader, _ int64) error {
	a.mu.Lock()
	a.started = append(a.started, p)
	a.inFlight++
	a.maxFlight = max(a.maxFlight, a.inFlight)
	if a.inFlight == cpTransferWorkers {
		close(a.full)
	}
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		a.inFlight--
		a.mu.Unlock()
	}()
	if _, err := io.ReadAll(r); err != nil {
		return err
	}
	// 失败的那条等到窗口占满才失败（等不到就照样失败，串行实现在这里红）；其余条目
	// 一直写到被取消为止——它们只能被首个错误取消。
	if p == a.fail {
		select {
		case <-a.full:
		case <-time.After(2 * time.Second):
		}
		return errors.New("cpgate: disk full")
	}
	select {
	case <-ctx.Done():
		a.mu.Lock()
		a.canceled++
		a.mu.Unlock()
		return ctx.Err()
	case <-time.After(3 * time.Second):
		return errors.New("cpgate: never canceled")
	}
}

// TestCpMultiSourceFirstErrorCancelsTheRest：首个失败照常返回并点名那一条，同时在写的
// 其余条目全部被取消，窗口之外的条目一个都不再开始。
func TestCpMultiSourceFirstErrorCancelsTheRest(t *testing.T) {
	cpConcAssets(t)
	cpGate.reset("/backup/f003.log")
	for _, name := range cpSourceNames(30) {
		p := "/src/" + name
		cpGate.entries = append(cpGate.entries, helper.Entry{Path: p, RelPath: name, Size: int64(len(name))})
		cpGate.contents[p] = []byte(name)
	}
	ctx := permission.WithPreapproved(context.Background())

	_, err := handleCp(ctx, map[string]any{"src": "gate-01:/src/", "dst": "gate-01:/backup/", "recursive": true})

	if err == nil {
		t.Fatal("cp succeeded, want the first write error")
	}
	if !strings.Contains(err.Error(), "disk full") || !strings.Contains(err.Error(), "/src/f003.log") ||
		!strings.Contains(err.Error(), "0/30") {
		t.Errorf("error = %v, want 0/30 and the failing entry /src/f003.log with its cause", err)
	}
	cpGate.mu.Lock()
	defer cpGate.mu.Unlock()
	if cpGate.maxFlight != cpTransferWorkers {
		t.Errorf("max writes in flight = %d, want %d", cpGate.maxFlight, cpTransferWorkers)
	}
	if len(cpGate.started) != cpTransferWorkers {
		sort.Strings(cpGate.started)
		t.Errorf("started %d writes %v, want only the %d in flight when the first error hit",
			len(cpGate.started), cpGate.started, cpTransferWorkers)
	}
	if cpGate.canceled != cpTransferWorkers-1 {
		t.Errorf("canceled writes = %d, want %d", cpGate.canceled, cpTransferWorkers-1)
	}
}
