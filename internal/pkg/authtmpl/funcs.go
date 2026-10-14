package authtmpl

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/url"

	"github.com/google/uuid"
)

// funcSpec 描述一个内置函数：参数个数与求值实现。参数、返回值都是原始字节：
// 哈希/HMAC 返回摘要字节，编码函数返回编码后文本的字节。
type funcSpec struct {
	// arity 是参数个数；variadic 为 true 时是下限，可以传更多。
	arity    int
	variadic bool
	call     func(args [][]byte) ([]byte, error)
}

// accepts 判断 n 个参数是否符合这个函数的参数个数。
func (s funcSpec) accepts(n int) bool {
	if s.variadic {
		return n >= s.arity
	}
	return n == s.arity
}

var builtinFuncs = map[string]funcSpec{
	"base64":      {arity: 1, variadic: true, call: base64Func},
	"hex":         {arity: 1, call: hexFunc},
	"urlencode":   {arity: 1, call: urlencodeFunc},
	"sha256":      {arity: 1, call: sha256Func},
	"hmac_sha256": {arity: 2, call: hmacSha256Func},
	"uuid":        {arity: 0, call: uuidFunc},
}

// base64Func 先按顺序拼接全部参数再编码：base64(a, b) 等同于 base64(a + b)。
func base64Func(args [][]byte) ([]byte, error) {
	return []byte(base64.StdEncoding.EncodeToString(bytes.Join(args, nil))), nil
}

func hexFunc(args [][]byte) ([]byte, error) {
	return []byte(hex.EncodeToString(args[0])), nil
}

func urlencodeFunc(args [][]byte) ([]byte, error) {
	return []byte(url.QueryEscape(string(args[0]))), nil
}

func sha256Func(args [][]byte) ([]byte, error) {
	sum := sha256.Sum256(args[0])
	return sum[:], nil
}

func hmacSha256Func(args [][]byte) ([]byte, error) {
	key, msg := args[0], args[1]
	mac := hmac.New(sha256.New, key)
	if _, err := mac.Write(msg); err != nil {
		return nil, fmt.Errorf("authtmpl: hmac_sha256: %w", err)
	}
	return mac.Sum(nil), nil
}

func uuidFunc([][]byte) ([]byte, error) {
	return []byte(uuid.New().String()), nil
}
