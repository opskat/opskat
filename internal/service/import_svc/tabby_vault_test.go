package import_svc

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"golang.org/x/crypto/pbkdf2"
)

// encryptForTest 用于测试的加密函数（与 Tabby 加密逻辑一致）
func encryptForTest(plaintext, passphrase string) *tabbyStoredVault {
	salt := make([]byte, 8)
	_, _ = rand.Read(salt)

	iv := make([]byte, aes.BlockSize)
	_, _ = rand.Read(iv)

	key := pbkdf2.Key([]byte(passphrase), salt, pbkdfIterations, cryptKeyLength, sha512.New)

	block, _ := aes.NewCipher(key)
	// PKCS7 padding
	padLen := aes.BlockSize - len(plaintext)%aes.BlockSize
	padded := make([]byte, len(plaintext)+padLen)
	copy(padded, plaintext)
	for i := len(plaintext); i < len(padded); i++ {
		padded[i] = byte(padLen)
	}

	ciphertext := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(ciphertext, padded)

	return &tabbyStoredVault{
		Version:  1,
		Contents: base64.StdEncoding.EncodeToString(ciphertext),
		KeySalt:  hex.EncodeToString(salt),
		IV:       hex.EncodeToString(iv),
	}
}

func TestDecryptTabbyVault(t *testing.T) {
	Convey("Tabby vault 解密", t, func() {
		Convey("正确密码可以解密", func() {
			vault := tabbyVault{
				Secrets: []tabbySecret{
					{Type: "ssh:password", Key: json.RawMessage(`"profile-uuid-1"`), Value: "my-password"},
					{Type: "ssh:password", Key: json.RawMessage(`"profile-uuid-2"`), Value: "another-password"},
				},
			}
			plaintext, _ := json.Marshal(vault)
			stored := encryptForTest(string(plaintext), "test-passphrase")

			result, err := decryptTabbyVault(stored, "test-passphrase")
			So(err, ShouldBeNil)
			So(len(result.Secrets), ShouldEqual, 2)
			So(result.Secrets[0].Type, ShouldEqual, "ssh:password")
			So(result.Secrets[0].secretKey(), ShouldEqual, "profile-uuid-1")
			So(result.Secrets[0].Value, ShouldEqual, "my-password")
			So(result.Secrets[1].secretKey(), ShouldEqual, "profile-uuid-2")
			So(result.Secrets[1].Value, ShouldEqual, "another-password")
		})

		Convey("错误密码返回错误", func() {
			vault := tabbyVault{
				Secrets: []tabbySecret{
					{Type: "ssh:password", Key: json.RawMessage(`"uuid-1"`), Value: "secret"},
				},
			}
			plaintext, _ := json.Marshal(vault)
			stored := encryptForTest(string(plaintext), "correct-passphrase")

			_, err := decryptTabbyVault(stored, "wrong-passphrase")
			So(err, ShouldNotBeNil)
		})

		Convey("空 vault 返回错误", func() {
			_, err := decryptTabbyVault(nil, "test")
			So(err, ShouldNotBeNil)

			_, err = decryptTabbyVault(&tabbyStoredVault{}, "test")
			So(err, ShouldNotBeNil)
		})
	})
}

func TestBuildVaultPasswordMap(t *testing.T) {
	Convey("构建 vault 密码映射", t, func() {
		Convey("正确映射 ssh:password 类型", func() {
			vault := &tabbyVault{
				Secrets: []tabbySecret{
					{Type: "ssh:password", Key: json.RawMessage(`"profile-1"`), Value: "pass1"},
					{Type: "ssh:password", Key: json.RawMessage(`"profile-2"`), Value: "pass2"},
					{Type: "ssh:key-passphrase", Key: json.RawMessage(`"profile-3"`), Value: "keypass"},
					{Type: "other:type", Key: json.RawMessage(`"profile-4"`), Value: "ignored"},
				},
			}

			passwords := buildVaultPasswordMap(vault)
			So(passwords, ShouldHaveLength, 3)
			So(passwords["profile-1"], ShouldEqual, "pass1")
			So(passwords["profile-2"], ShouldEqual, "pass2")
			So(passwords["profile-3"], ShouldEqual, "keypass")
			_, exists := passwords["profile-4"]
			So(exists, ShouldBeFalse)
		})

		Convey("对象格式 key 可以解析", func() {
			vault := &tabbyVault{
				Secrets: []tabbySecret{
					{Type: "ssh:password", Key: json.RawMessage(`{"id":"file-uuid","description":"test.pem"}`), Value: "filepass"},
				},
			}

			passwords := buildVaultPasswordMap(vault)
			So(passwords["file-uuid"], ShouldEqual, "filepass")
		})

		Convey("空值被忽略", func() {
			vault := &tabbyVault{
				Secrets: []tabbySecret{
					{Type: "ssh:password", Key: json.RawMessage(`"profile-1"`), Value: ""},
					{Type: "ssh:password", Key: json.RawMessage(`""`), Value: "pass"},
				},
			}

			passwords := buildVaultPasswordMap(vault)
			So(passwords, ShouldHaveLength, 0)
		})
	})
}

func TestPkcs7Unpad(t *testing.T) {
	Convey("PKCS7 unpadding", t, func() {
		Convey("正确去除填充", func() {
			data := []byte("hello world!")
			padLen := aes.BlockSize - len(data)%aes.BlockSize
			padded := make([]byte, len(data)+padLen)
			copy(padded, data)
			for i := len(data); i < len(padded); i++ {
				padded[i] = byte(padLen)
			}

			result, err := pkcs7Unpad(padded)
			So(err, ShouldBeNil)
			So(string(result), ShouldEqual, "hello world!")
		})

		Convey("整块填充", func() {
			data := make([]byte, aes.BlockSize)
			for i := range data {
				data[i] = 'A'
			}
			padded := make([]byte, aes.BlockSize*2)
			copy(padded, data)
			for i := aes.BlockSize; i < len(padded); i++ {
				padded[i] = byte(aes.BlockSize)
			}

			result, err := pkcs7Unpad(padded)
			So(err, ShouldBeNil)
			So(len(result), ShouldEqual, aes.BlockSize)
		})

		Convey("空数据返回错误", func() {
			_, err := pkcs7Unpad([]byte{})
			So(err, ShouldNotBeNil)
		})

		Convey("无效 padding 返回错误", func() {
			data := []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 0}
			_, err := pkcs7Unpad(data)
			So(err, ShouldNotBeNil)
		})
	})
}
