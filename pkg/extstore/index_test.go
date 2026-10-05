package extstore

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const sampleIndex = `{
  "format": 1,
  "extensions": [{
    "name": "es",
    "display": {
      "en": {"name": "Elasticsearch", "description": "Browse indices"},
      "zh-CN": {"name": "Elasticsearch", "description": "浏览索引"}
    },
    "icon": "database",
    "versions": [
      {
        "version": "1.0.0",
        "hostABI": "2.2",
        "minAppVersion": "1.14.0",
        "capabilities": {"http": {"allowlist": ["https://example.com/"]}, "network": {"assetEndpoint": true}},
        "size": 1024,
        "publishedAt": "2026-10-04T08:00:00Z",
        "source": {"type": "oci", "ref": "ghcr.io/opskat/extensions/es:1.0.0", "sha256": "abc123"}
      },
      {
        "version": "1.1.0",
        "hostABI": "2.2",
        "size": 2048,
        "publishedAt": "2026-10-05T08:00:00Z",
        "source": {"ref": "ghcr.io/opskat/extensions/es:1.1.0", "sha256": "def456"}
      }
    ]
  }]
}`

func TestParseIndex(t *testing.T) {
	t.Run("reads the wire format", func(t *testing.T) {
		idx, err := ParseIndex([]byte(sampleIndex))
		require.NoError(t, err)
		assert.Equal(t, 1, idx.Format)
		require.Len(t, idx.Extensions, 1)
		ext := idx.Extensions[0]
		assert.Equal(t, "es", ext.Name)
		assert.Equal(t, "database", ext.Icon)
		assert.Equal(t, Display{Name: "Elasticsearch", Description: "浏览索引"}, ext.Display["zh-CN"])
		require.Len(t, ext.Versions, 2)
		v := ext.Versions[0]
		assert.Equal(t, "1.0.0", v.Version)
		assert.Equal(t, "2.2", v.HostABI)
		assert.Equal(t, "1.14.0", v.MinAppVersion)
		assert.Equal(t, []string{"https://example.com/"}, v.Capabilities.HTTP.Allowlist)
		assert.True(t, v.Capabilities.Network.AssetEndpoint)
		assert.Equal(t, int64(1024), v.Size)
		assert.True(t, v.PublishedAt.Equal(time.Date(2026, 10, 4, 8, 0, 0, 0, time.UTC)))
		assert.Equal(t, Source{Type: SourceOCI, Ref: "ghcr.io/opskat/extensions/es:1.0.0", SHA256: "abc123"}, v.Source)
	})

	t.Run("missing source type means oci", func(t *testing.T) {
		idx, err := ParseIndex([]byte(sampleIndex))
		require.NoError(t, err)
		assert.Equal(t, SourceOCI, idx.Extensions[0].Versions[1].Source.Type)
	})

	t.Run("unknown format is a typed error even when the rest is unreadable", func(t *testing.T) {
		_, err := ParseIndex([]byte(`{"format": 2, "extensions": {"es": "a shape v1 cannot read"}}`))
		var fe *UnsupportedFormatError
		require.True(t, errors.As(err, &fe), "want UnsupportedFormatError, got %v", err)
		assert.Equal(t, 2, fe.Format)
	})

	t.Run("missing format is unsupported", func(t *testing.T) {
		_, err := ParseIndex([]byte(`{"extensions": []}`))
		var fe *UnsupportedFormatError
		require.True(t, errors.As(err, &fe), "want UnsupportedFormatError, got %v", err)
		assert.Equal(t, 0, fe.Format)
	})

	t.Run("malformed JSON is not reported as a format problem", func(t *testing.T) {
		_, err := ParseIndex([]byte(`{"format": 1, "extensions": [`))
		require.Error(t, err)
		var fe *UnsupportedFormatError
		assert.False(t, errors.As(err, &fe))
	})

	t.Run("an extension needs a name and a version", func(t *testing.T) {
		_, err := ParseIndex([]byte(`{"format": 1, "extensions": [{"name": "es", "versions": []}]}`))
		require.Error(t, err)
		_, err = ParseIndex([]byte(`{"format": 1, "extensions": [{"name": "", "versions": [{"version": "1.0.0"}]}]}`))
		require.Error(t, err)
		_, err = ParseIndex([]byte(`{"format": 1, "extensions": [{"name": "es", "versions": [{"version": ""}]}]}`))
		require.Error(t, err)
	})
}

func newKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	return pub, priv
}

func TestSignVerify(t *testing.T) {
	raw := []byte(sampleIndex)
	pub, priv := newKey(t)
	otherPub, otherPriv := newKey(t)

	t.Run("sign output is base64 text of an ed25519 signature over the raw bytes", func(t *testing.T) {
		sig := Sign(raw, priv)
		decoded, err := base64.StdEncoding.DecodeString(string(sig))
		require.NoError(t, err)
		assert.True(t, ed25519.Verify(pub, raw, decoded))
	})

	t.Run("valid signature is accepted", func(t *testing.T) {
		require.NoError(t, Verify(raw, Sign(raw, priv), []ed25519.PublicKey{pub}))
	})

	t.Run("any trusted key may sign (rotation)", func(t *testing.T) {
		keys := []ed25519.PublicKey{pub, otherPub}
		require.NoError(t, Verify(raw, Sign(raw, priv), keys))
		require.NoError(t, Verify(raw, Sign(raw, otherPriv), keys))
	})

	t.Run("trailing newline in the sig file is tolerated", func(t *testing.T) {
		sig := append(Sign(raw, priv), '\n')
		require.NoError(t, Verify(raw, sig, []ed25519.PublicKey{pub}))
	})

	t.Run("any tampered byte is rejected", func(t *testing.T) {
		sig := Sign(raw, priv)
		for i := range raw {
			tampered := append([]byte(nil), raw...)
			tampered[i] ^= 0x01
			err := Verify(tampered, sig, []ed25519.PublicKey{pub})
			require.ErrorIs(t, err, ErrInvalidSignature, "byte %d", i)
		}
	})

	t.Run("signature from an untrusted key is rejected", func(t *testing.T) {
		err := Verify(raw, Sign(raw, otherPriv), []ed25519.PublicKey{pub})
		require.ErrorIs(t, err, ErrInvalidSignature)
	})

	t.Run("missing signature is rejected as missing", func(t *testing.T) {
		require.ErrorIs(t, Verify(raw, nil, []ed25519.PublicKey{pub}), ErrMissingSignature)
		require.ErrorIs(t, Verify(raw, []byte(" \n"), []ed25519.PublicKey{pub}), ErrMissingSignature)
	})

	t.Run("garbage signature is rejected", func(t *testing.T) {
		require.ErrorIs(t, Verify(raw, []byte("not base64!"), []ed25519.PublicKey{pub}), ErrInvalidSignature)
		short := []byte(base64.StdEncoding.EncodeToString([]byte("short")))
		require.ErrorIs(t, Verify(raw, short, []ed25519.PublicKey{pub}), ErrInvalidSignature)
	})

	t.Run("no trusted keys accepts nothing", func(t *testing.T) {
		require.ErrorIs(t, Verify(raw, Sign(raw, priv), nil), ErrInvalidSignature)
	})

	t.Run("a malformed trusted key is an error, not a panic", func(t *testing.T) {
		err := Verify(raw, Sign(raw, priv), []ed25519.PublicKey{ed25519.PublicKey("short")})
		require.Error(t, err)
	})
}
