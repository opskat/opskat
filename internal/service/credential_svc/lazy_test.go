package credential_svc

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

func TestNewLazy_DefersResolveUntilFirstUse(t *testing.T) {
	salt := []byte("0123456789abcdef")
	var calls atomic.Int32
	svc := NewLazy(func() (string, error) {
		calls.Add(1)
		return "lazy-key", nil
	}, salt)
	if n := calls.Load(); n != 0 {
		t.Fatalf("构造时不应解析 master key，实际调用 %d 次", n)
	}
	enc, err := svc.Encrypt("secret")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("首次 Encrypt 后应解析 1 次，实际 %d", n)
	}
	// 同一 key + salt 与 New 的结果互通
	if got, err := New("lazy-key", salt).Decrypt(enc); err != nil || got != "secret" {
		t.Fatalf("New 路径解密 = %q, %v", got, err)
	}
	other, err := New("lazy-key", salt).Encrypt("x")
	if err != nil {
		t.Fatalf("New Encrypt: %v", err)
	}
	if got, err := svc.Decrypt(other); err != nil || got != "x" {
		t.Fatalf("懒路径解密 = %q, %v", got, err)
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("多次使用只应解析 1 次，实际 %d", n)
	}
}

func TestNewLazy_ConcurrentFirstUseResolvesOnce(t *testing.T) {
	var calls atomic.Int32
	svc := NewLazy(func() (string, error) {
		calls.Add(1)
		return "k", nil
	}, []byte("0123456789abcdef"))
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := svc.Encrypt("v"); err != nil {
				t.Errorf("Encrypt: %v", err)
			}
		}()
	}
	wg.Wait()
	if n := calls.Load(); n != 1 {
		t.Fatalf("并发首次调用应只解析 1 次，实际 %d", n)
	}
}

func TestNewLazy_ResolveErrorSurfacesFromEncryptAndDecrypt(t *testing.T) {
	boom := errors.New("keychain boom")
	var calls atomic.Int32
	svc := NewLazy(func() (string, error) {
		calls.Add(1)
		return "", boom
	}, []byte("0123456789abcdef"))
	if _, err := svc.Encrypt("v"); !errors.Is(err, boom) {
		t.Fatalf("Encrypt err = %v, 期望包含 boom", err)
	}
	if _, err := svc.Decrypt("AAAA"); !errors.Is(err, boom) {
		t.Fatalf("Decrypt err = %v, 期望包含 boom", err)
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("错误应被缓存，解析只应 1 次，实际 %d", n)
	}
}
