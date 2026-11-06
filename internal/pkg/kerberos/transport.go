package kerberos

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/jcmturner/gokrb5/v8/messages"
)

const (
	// kdcTimeout 是与一个 KDC 的一次交换（拨号 + 收发）的时限。
	kdcTimeout = 10 * time.Second
	// maxReplySize 是 KDC 应答的长度上限，挡住异常长度前缀导致的大块分配。
	maxReplySize = 1 << 20
)

// send 按顺序把请求发给各 KDC，第一个连上并应答的为准；KDC 回 KRB-ERROR 时返回 *kdcError，不再试下一个。
// 都连不上时错误点名每个试过的地址与原因。返回应答与应答它的 KDC。
func (cl *client) send(ctx context.Context, dial DialFunc, req []byte, what string) ([]byte, string, error) {
	failures := make([]string, 0, len(cl.kdcs))
	for _, kdc := range cl.kdcs {
		rep, err := roundTrip(ctx, dial, kdc, req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, "", fmt.Errorf("kerberos: %s via KDC %s: %w", what, kdc, ctx.Err())
			}
			failures = append(failures, kdc+": "+err.Error())
			continue
		}
		var krbErr messages.KRBError
		if krbErr.Unmarshal(rep) == nil {
			return nil, kdc, &kdcError{kdc: kdc, what: what, krb: krbErr}
		}
		return rep, kdc, nil
	}
	return nil, "", fmt.Errorf("kerberos: no KDC reachable for %s (tried %s)", what, strings.Join(failures, "; "))
}

// roundTrip 经 dial 连 addr，按 RFC 4120 7.2.2 的 TCP 分帧（4 字节大端长度前缀）发一个请求、收一个应答。
func roundTrip(ctx context.Context, dial DialFunc, addr string, req []byte) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, kdcTimeout)
	defer cancel()
	conn, err := dial(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()
	deadline, _ := ctx.Deadline()
	if err := conn.SetDeadline(deadline); err != nil {
		return nil, err
	}
	// 取消时让阻塞中的读写立刻返回
	stop := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Now()) })
	defer stop()

	frame := make([]byte, 4+len(req))
	binary.BigEndian.PutUint32(frame, uint32(len(req)))
	copy(frame[4:], req)
	if _, err := conn.Write(frame); err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	var size [4]byte
	if _, err := io.ReadFull(conn, size[:]); err != nil {
		return nil, fmt.Errorf("read reply: %w", err)
	}
	n := binary.BigEndian.Uint32(size[:])
	if n == 0 || n > maxReplySize {
		return nil, fmt.Errorf("reply length %d is out of range", n)
	}
	rep := make([]byte, n)
	if _, err := io.ReadFull(conn, rep); err != nil {
		return nil, fmt.Errorf("read reply: %w", err)
	}
	return rep, nil
}
