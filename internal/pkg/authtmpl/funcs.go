package authtmpl

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/url"

	"github.com/google/uuid"
)

// funcSpec 描述一个内置函数：固定参数个数与求值实现。参数、返回值都是原始
// 字节：哈希/HMAC 返回摘要字节，编码函数返回编码后文本的字节。
type funcSpec struct {
	arity int
	call  func(args [][]byte) ([]byte, error)
}

var builtinFuncs = map[string]funcSpec{
	"base64":      {arity: 1, call: base64Func},
	"hex":         {arity: 1, call: hexFunc},
	"urlencode":   {arity: 1, call: urlencodeFunc},
	"sha256":      {arity: 1, call: sha256Func},
	"hmac_sha256": {arity: 2, call: hmacSha256Func},
	"uuid":        {arity: 0, call: uuidFunc},
}

func base64Func(args [][]byte) ([]byte, error) {
	return []byte(base64.StdEncoding.EncodeToString(args[0])), nil
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
