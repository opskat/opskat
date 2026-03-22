package credential_svc

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
)

// CredentialSvc 凭证加解密服务（AES-256-GCM）
type CredentialSvc struct {
	gcm cipher.AEAD
}

// New 创建凭证服务，masterKey 用于派生 AES 密钥
func New(masterKey string) *CredentialSvc {
	// 用 SHA-256 派生固定 32 字节密钥
	hash := sha256.Sum256([]byte(masterKey))
	block, err := aes.NewCipher(hash[:])
	if err != nil {
		panic(fmt.Sprintf("创建 AES cipher 失败: %v", err))
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		panic(fmt.Sprintf("创建 GCM 失败: %v", err))
	}
	return &CredentialSvc{gcm: gcm}
}

// Encrypt 加密明文，返回 base64 编码的密文
func (s *CredentialSvc) Encrypt(plaintext string) (string, error) {
	nonce := make([]byte, s.gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("生成 nonce 失败: %w", err)
	}
	ciphertext := s.gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// Decrypt 解密 base64 编码的密文
func (s *CredentialSvc) Decrypt(ciphertextB64 string) (string, error) {
	data, err := base64.StdEncoding.DecodeString(ciphertextB64)
	if err != nil {
		return "", fmt.Errorf("base64 解码失败: %w", err)
	}
	nonceSize := s.gcm.NonceSize()
	if len(data) < nonceSize {
		return "", fmt.Errorf("密文太短")
	}
	nonce, ciphertext := data[:nonceSize], data[nonceSize:]
	plaintext, err := s.gcm.Open(nil, nonce, ciphertext, nil)
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
