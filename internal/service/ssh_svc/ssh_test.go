package ssh_svc

import (
	"testing"

	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
)

func TestManager_Basic(t *testing.T) {
	convey.Convey("SSH Manager 基础功能", t, func() {
		m := NewManager()

		convey.Convey("新创建的 Manager 无活跃会话", func() {
			assert.Equal(t, 0, m.ActiveSessions())
		})

		convey.Convey("获取不存在的会话返回 false", func() {
			_, ok := m.GetSession("nonexistent")
			assert.False(t, ok)
		})

		convey.Convey("断开不存在的会话不 panic", func() {
			assert.NotPanics(t, func() {
				m.Disconnect("nonexistent")
			})
		})

		convey.Convey("DisconnectAll 空管理器不 panic", func() {
			assert.NotPanics(t, func() {
				m.DisconnectAll()
			})
		})
	})
}

func TestManager_ConnectInvalidAuth(t *testing.T) {
	convey.Convey("SSH 连接无效参数", t, func() {
		m := NewManager()

		convey.Convey("不支持的认证方式返回错误", func() {
			_, err := m.Connect(ConnectConfig{
				Host:     "127.0.0.1",
				Port:     22,
				Username: "root",
				AuthType: "unsupported",
				OnData:   func(string, []byte) {},
				OnClosed: func(string) {},
			})
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "不支持的认证方式")
		})

		convey.Convey("无效密钥返回错误", func() {
			_, err := m.Connect(ConnectConfig{
				Host:     "127.0.0.1",
				Port:     22,
				Username: "root",
				AuthType: "key",
				Key:      "invalid-key-content",
				OnData:   func(string, []byte) {},
				OnClosed: func(string) {},
			})
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "解析密钥失败")
		})
	})
}

func TestSession_ClosedBehavior(t *testing.T) {
	convey.Convey("Session 关闭后的行为", t, func() {
		// 创建一个模拟的 closed session 来测试
		sess := &Session{
			ID:     "test-1",
			closed: true,
		}

		convey.Convey("关闭的 session Write 返回错误", func() {
			err := sess.Write([]byte("test"))
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "closed")
		})

		convey.Convey("关闭的 session Resize 返回错误", func() {
			err := sess.Resize(80, 24)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "closed")
		})

		convey.Convey("IsClosed 返回 true", func() {
			assert.True(t, sess.IsClosed())
		})

		convey.Convey("重复 Close 不 panic", func() {
			assert.NotPanics(t, func() {
				sess.Close()
			})
		})
	})
}
