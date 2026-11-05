package credential_svc

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"sync"

	"golang.org/x/crypto/argon2"
)

// Argon2id 参数（与 backup_svc 保持一致）
const (
	argon2Time    = 3
	argon2Memory  = 64 * 1024 // 64 MiB
	argon2Threads = 4
	argon2KeyLen  = 32 // AES-256
	saltLen       = 16
)

// CredentialSvc 凭证加解密服务（Argon2id + AES-256-GCM）。
// master key 解析与 Argon2id 派生推迟到首次 Encrypt / Decrypt，并发首次调用只执行一次；
// 解析或派生失败的错误会被缓存并由 Encrypt / Decrypt 返回。
type CredentialSvc struct {
	resolveKey func() (string, error)
	salt       []byte

	once sync.Once
	gcm  cipher.AEAD
	err  error
}

// NewLazy 创建凭证服务，master key 由 resolveKey 在首次加解密时解析，
// 随后使用 Argon2id(masterKey, salt) 派生 AES-256 密钥
func NewLazy(resolveKey func() (string, error), salt []byte) *CredentialSvc {
	return &CredentialSvc{resolveKey: resolveKey, salt: salt}
}

// New 创建凭证服务，使用 Argon2id(masterKey, salt) 派生 AES-256 密钥（首次加解密时才派生）
func New(masterKey string, salt []byte) *CredentialSvc {
	return NewLazy(func() (string, error) { return masterKey, nil }, salt)
}

// aead 返回派生好的 GCM，首次调用时解析 master key 并派生密钥
func (s *CredentialSvc) aead() (cipher.AEAD, error) {
	s.once.Do(func() {
		masterKey, err := s.resolveKey()
		if err != nil {
			s.err = fmt.Errorf("获取 master key 失败: %w", err)
			return
		}
		key := argon2.IDKey([]byte(masterKey), s.salt, argon2Time, argon2Memory, argon2Threads, argon2KeyLen)
		defer func() {
			// 清除密钥材料
			for i := range key {
				key[i] = 0
			}
		}()
		block, err := aes.NewCipher(key)
		if err != nil {
			s.err = fmt.Errorf("创建 AES cipher 失败: %w", err)
			return
		}
		s.gcm, s.err = cipher.NewGCM(block)
		if s.err != nil {
			s.err = fmt.Errorf("创建 GCM 失败: %w", s.err)
		}
	})
	return s.gcm, s.err
}

// GenerateSalt 生成随机 salt
func GenerateSalt() ([]byte, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("生成 salt 失败: %w", err)
	}
	return salt, nil
}

// Encrypt 加密明文，返回 base64 编码的密文
func (s *CredentialSvc) Encrypt(plaintext string) (string, error) {
	gcm, err := s.aead()
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("生成 nonce 失败: %w", err)
	}
	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// Decrypt 解密 base64 编码的密文
func (s *CredentialSvc) Decrypt(ciphertextB64 string) (string, error) {
	gcm, err := s.aead()
	if err != nil {
		return "", err
	}
	data, err := base64.StdEncoding.DecodeString(ciphertextB64)
	if err != nil {
		return "", fmt.Errorf("base64 解码失败: %w", err)
	}
	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return "", fmt.Errorf("密文太短")
	}
	nonce, ciphertext := data[:nonceSize], data[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("解密失败: %w", err)
	}
	return string(plaintext), nil
}

// 全局单例
var defaultSvc *CredentialSvc

// SetDefault 设置全局实例
func SetDefault(svc *CredentialSvc) {
	defaultSvc = svc
}

// Default 获取全局实例
func Default() *CredentialSvc {
	return defaultSvc
}
