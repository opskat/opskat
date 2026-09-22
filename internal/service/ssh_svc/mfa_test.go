package ssh_svc

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"io"
	"testing"

	"github.com/opskat/opskat/internal/sshagent"

	"github.com/smartystreets/goconvey/convey"
	"golang.org/x/crypto/ssh"
)

// otpChallenge 是服务器端的一轮单提示 OTP 挑战：答案必须等于 want。
func otpChallenge(want string) func(ssh.ConnMetadata, ssh.KeyboardInteractiveChallenge) (*ssh.Permissions, error) {
	return func(_ ssh.ConnMetadata, ch ssh.KeyboardInteractiveChallenge) (*ssh.Permissions, error) {
		answers, err := ch("Verification", "Enter your one-time code", []string{"OTP: "}, []bool{false})
		if err != nil {
			return nil, err
		}
		if len(answers) != 1 || answers[0] != want {
			return nil, fmt.Errorf("bad otp")
		}
		return nil, nil
	}
}

func newTestPrivateKeyPEM(t *testing.T) string {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	return string(pem.EncodeToMemory(block))
}

func dialForMFATest(t *testing.T, srv *controllableSSHServer, cfg ConnectConfig) error {
	t.Helper()
	host, port := srv.hostPort()
	cfg.Host, cfg.Port, cfg.Username = host, port, "alice"
	cfg.HostKeyVerifyFunc = AutoTrustFirstRejectChangeVerifyFunc()
	client, closers, err := NewManager().Dial(cfg)
	if err != nil {
		return err
	}
	_ = client.Close()
	for _, c := range closers {
		func(c io.Closer) { _ = c.Close() }(c)
	}
	return nil
}

func TestManagerDialMFAResponder(t *testing.T) {
	setupAgentFactoryTest(t)
	convey.Convey("非 Agent 认证经 ConnectConfig.MFA 完成 keyboard-interactive 二次验证", t, func() {
		convey.Convey("私钥部分成功后 OTP 挑战交给应答方", func() {
			srv, _ := newControllableSSHServer(t, &ssh.ServerConfig{
				PublicKeyCallback: func(ssh.ConnMetadata, ssh.PublicKey) (*ssh.Permissions, error) {
					return nil, &ssh.PartialSuccessError{Next: ssh.ServerAuthCallbacks{
						KeyboardInteractiveCallback: otpChallenge("123456"),
					}}
				},
			})
			caller := &recordingAgentCaller{answers: []string{"123456"}}
			err := dialForMFATest(t, srv, ConnectConfig{AuthType: "key", Key: newTestPrivateKeyPEM(t), MFA: caller})
			convey.So(err, convey.ShouldBeNil)
			convey.So(caller.challengeCount(), convey.ShouldEqual, 1)
			convey.So(caller.first(), convey.ShouldResemble, sshagent.MFAChallenge{
				Name: "Verification", Instruction: "Enter your one-time code",
				Prompts: []string{"OTP: "}, Echo: []bool{false},
			})
		})

		convey.Convey("密码方法部分成功后，OTP 轮不再拿已保存密码作答", func() {
			srv, _ := newControllableSSHServer(t, &ssh.ServerConfig{
				PasswordCallback: func(_ ssh.ConnMetadata, pw []byte) (*ssh.Permissions, error) {
					if string(pw) != "secret" {
						return nil, fmt.Errorf("bad password")
					}
					return nil, &ssh.PartialSuccessError{Next: ssh.ServerAuthCallbacks{
						KeyboardInteractiveCallback: otpChallenge("654321"),
					}}
				},
			})
			caller := &recordingAgentCaller{answers: []string{"654321"}}
			err := dialForMFATest(t, srv, ConnectConfig{AuthType: "password", Password: "secret", MFA: caller})
			convey.So(err, convey.ShouldBeNil)
			convey.So(caller.challengeCount(), convey.ShouldEqual, 1)
		})

		convey.Convey("只开 keyboard-interactive 的服务器：首轮密码提示用已保存密码，后续轮交给应答方", func() {
			srv, _ := newControllableSSHServer(t, &ssh.ServerConfig{
				KeyboardInteractiveCallback: func(_ ssh.ConnMetadata, ch ssh.KeyboardInteractiveChallenge) (*ssh.Permissions, error) {
					pw, err := ch("", "", []string{"Password: "}, []bool{false})
					if err != nil {
						return nil, err
					}
					if len(pw) != 1 || pw[0] != "secret" {
						return nil, fmt.Errorf("bad password")
					}
					return otpChallenge("111222")(nil, ch)
				},
			})
			caller := &recordingAgentCaller{answers: []string{"111222"}}
			err := dialForMFATest(t, srv, ConnectConfig{AuthType: "password", Password: "secret", MFA: caller})
			convey.So(err, convey.ShouldBeNil)
			convey.So(caller.challengeCount(), convey.ShouldEqual, 1)
			convey.So(caller.first().Prompts, convey.ShouldResemble, []string{"OTP: "})
		})

		convey.Convey("服务器拒绝应答方的答案时报 MFA 验证失败，而不是笼统的认证失败", func() {
			srv, _ := newControllableSSHServer(t, &ssh.ServerConfig{
				PublicKeyCallback: func(ssh.ConnMetadata, ssh.PublicKey) (*ssh.Permissions, error) {
					return nil, &ssh.PartialSuccessError{Next: ssh.ServerAuthCallbacks{
						KeyboardInteractiveCallback: otpChallenge("123456"),
					}}
				},
			})
			caller := &recordingAgentCaller{answers: []string{"000000"}}
			err := dialForMFATest(t, srv, ConnectConfig{AuthType: "key", Key: newTestPrivateKeyPEM(t), MFA: caller})
			convey.So(err, convey.ShouldNotBeNil)
			code, ok := sshagent.CodeOf(err)
			convey.So(ok, convey.ShouldBeTrue)
			convey.So(code, convey.ShouldEqual, sshagent.CodeMFAFailed)
			convey.So(err.Error(), convey.ShouldNotContainSubstring, "000000")
		})

		convey.Convey("应答方返回的类型化错误原样保留在拨号错误里", func() {
			srv, _ := newControllableSSHServer(t, &ssh.ServerConfig{
				PublicKeyCallback: func(ssh.ConnMetadata, ssh.PublicKey) (*ssh.Permissions, error) {
					return nil, &ssh.PartialSuccessError{Next: ssh.ServerAuthCallbacks{
						KeyboardInteractiveCallback: otpChallenge("123456"),
					}}
				},
			})
			caller := refusingCaller{err: &sshagent.Error{Code: sshagent.CodeMFARequired, Message: "no responder"}}
			err := dialForMFATest(t, srv, ConnectConfig{AuthType: "key", Key: newTestPrivateKeyPEM(t), MFA: caller})
			convey.So(err, convey.ShouldNotBeNil)
			code, ok := sshagent.CodeOf(err)
			convey.So(ok, convey.ShouldBeTrue)
			convey.So(code, convey.ShouldEqual, sshagent.CodeMFARequired)
		})
	})
}

type refusingCaller struct{ err error }

func (r refusingCaller) SubmitChallenge(_ context.Context, _ sshagent.MFAChallenge) ([]string, error) {
	return nil, r.err
}
