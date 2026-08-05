package sshagent

import (
	"context"
	"strings"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

func TestCopyPublicKey(t *testing.T) {
	Convey("CopyPublicKey 重读来源并按指纹返回公钥", t, func() {
		Convey("返回不含 Agent 备注的 authorized key 行", func() {
			priv, pub := newTestKey(t)
			srv, _ := keyringServer(t, agent.AddedKey{PrivateKey: priv, Comment: "secret comment"})

			line, err := CopyPublicKey(context.Background(), Source{Type: EndpointTypeUnixSocket, Value: srv.path}, FingerprintSHA256(pub))
			So(err, ShouldBeNil)
			So(line, ShouldContainSubstring, "ssh-ed25519")
			So(line, ShouldNotContainSubstring, "secret comment")

			got, _, _, _, err := ssh.ParseAuthorizedKey([]byte(line))
			So(err, ShouldBeNil)
			So(FingerprintSHA256(got), ShouldEqual, FingerprintSHA256(pub))
		})

		Convey("指纹不匹配任何身份 → ssh_agent_identity_missing", func() {
			priv, _ := newTestKey(t)
			srv, _ := keyringServer(t, agent.AddedKey{PrivateKey: priv, Comment: "k"})

			_, err := CopyPublicKey(context.Background(), Source{Type: EndpointTypeUnixSocket, Value: srv.path}, "SHA256:"+strings.Repeat("A", 43))
			So(errCode(err), ShouldEqual, CodeIdentityMissing)
		})

		Convey("同一公钥出现两次 → ssh_agent_identity_duplicate", func() {
			// A real keyring deduplicates identical keys, so craft a raw
			// identities reply carrying the same blob twice.
			_, pub := newTestKey(t)
			pubBlob := pub.Marshal()
			body := identitiesResponse(2, keyRecord(pubBlob, "a"), keyRecord(pubBlob, "b"))
			srv := newUnixAgentServer(t, respondThenWaitClose(body))

			_, err := CopyPublicKey(context.Background(), Source{Type: EndpointTypeUnixSocket, Value: srv.path}, FingerprintSHA256(pub))
			So(errCode(err), ShouldEqual, CodeIdentityDuplicate)
		})

		Convey("端点不可达 → 类型化错误", func() {
			_, err := CopyPublicKey(context.Background(), Source{Type: EndpointTypeUnixSocket, Value: "/tmp/not-a-socket-opskat"}, "SHA256:xxxxxxxx")
			So(errCode(err), ShouldEqual, CodeEndpointUnavailable)
		})
	})
}
