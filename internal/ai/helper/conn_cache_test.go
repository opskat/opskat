package helper

import (
	"errors"
	"io"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
)

// mockCloser 用于测试的简单 Closer
type mockCloser struct {
	closed bool
}

func (m *mockCloser) Close() error {
	m.closed = true
	return nil
}

func TestConnCache(t *testing.T) {
	Convey("ConnCache 泛型连接缓存", t, func() {
		cache := NewConnCache[*mockCloser]("test")

		Convey("GetOrDial - 第一次调用执行 dial 并缓存，返回 nil closer", func() {
			client := &mockCloser{}
			dialCalled := 0
			dial := func() (*mockCloser, io.Closer, error) {
				dialCalled++
				return client, nil, nil
			}

			got, closer, err := cache.GetOrDial(1, dial)

			assert.NoError(t, err)
			assert.Equal(t, client, got)
			assert.Nil(t, closer)
			assert.Equal(t, 1, dialCalled)
		})

		Convey("GetOrDial - 相同 assetID 第二次调用返回缓存，不再执行 dial", func() {
			client := &mockCloser{}
			dialCalled := 0
			dial := func() (*mockCloser, io.Closer, error) {
				dialCalled++
				return client, nil, nil
			}

			got1, _, err1 := cache.GetOrDial(1, dial)
			assert.NoError(t, err1)
			assert.Equal(t, client, got1)
			assert.Equal(t, 1, dialCalled)

			got2, closer2, err2 := cache.GetOrDial(1, dial)
			assert.NoError(t, err2)
			assert.Equal(t, client, got2)
			assert.Nil(t, closer2)
			assert.Equal(t, 1, dialCalled) // dial 不应再次被调用
		})

		Convey("GetOrDial - 不同 assetID 分别缓存", func() {
			client1 := &mockCloser{}
			client2 := &mockCloser{}
			dial1 := func() (*mockCloser, io.Closer, error) {
				return client1, nil, nil
			}
			dial2 := func() (*mockCloser, io.Closer, error) {
				return client2, nil, nil
			}

			got1, _, err1 := cache.GetOrDial(1, dial1)
			got2, _, err2 := cache.GetOrDial(2, dial2)

			assert.NoError(t, err1)
			assert.NoError(t, err2)
			assert.Equal(t, client1, got1)
			assert.Equal(t, client2, got2)
			assert.NotSame(t, got1, got2)
		})

		Convey("GetOrDial - dial 返回错误时传播错误", func() {
			dialErr := errors.New("connection refused")
			dial := func() (*mockCloser, io.Closer, error) {
				return nil, nil, dialErr
			}

			got, closer, err := cache.GetOrDial(1, dial)

			assert.Error(t, err)
			assert.Equal(t, dialErr, err)
			assert.Nil(t, got)
			assert.Nil(t, closer)
		})

		Convey("Close - 关闭所有缓存的客户端和 tunnel closer", func() {
			client1 := &mockCloser{}
			client2 := &mockCloser{}
			tunnel1 := &mockCloser{}

			dial1 := func() (*mockCloser, io.Closer, error) {
				return client1, tunnel1, nil
			}
			dial2 := func() (*mockCloser, io.Closer, error) {
				return client2, nil, nil
			}

			_, _, err1 := cache.GetOrDial(1, dial1)
			_, _, err2 := cache.GetOrDial(2, dial2)
			assert.NoError(t, err1)
			assert.NoError(t, err2)

			err := cache.Close()
			assert.NoError(t, err)
			assert.True(t, client1.closed)
			assert.True(t, client2.closed)
			assert.True(t, tunnel1.closed)
		})

		Convey("Remove - 关闭并移除指定 assetID 的缓存连接", func() {
			client1 := &mockCloser{}
			client2 := &mockCloser{}
			tunnel1 := &mockCloser{}

			dial1 := func() (*mockCloser, io.Closer, error) {
				return client1, tunnel1, nil
			}
			dial2 := func() (*mockCloser, io.Closer, error) {
				return client2, nil, nil
			}

			_, _, err1 := cache.GetOrDial(1, dial1)
			_, _, err2 := cache.GetOrDial(2, dial2)
			assert.NoError(t, err1)
			assert.NoError(t, err2)

			// Remove assetID=1，只关闭 client1 和 tunnel1
			cache.Remove(1)
			assert.True(t, client1.closed)
			assert.True(t, tunnel1.closed)
			assert.False(t, client2.closed)

			// 再次 GetOrDial(1) 应重新调用 dial
			newClient := &mockCloser{}
			dialCalled := 0
			dialNew := func() (*mockCloser, io.Closer, error) {
				dialCalled++
				return newClient, nil, nil
			}
			got, _, err := cache.GetOrDial(1, dialNew)
			assert.NoError(t, err)
			assert.Equal(t, newClient, got)
			assert.Equal(t, 1, dialCalled)
		})

		Convey("Remove - 对不存在的 assetID 无副作用", func() {
			cache.Remove(999) // 不应 panic
		})
	})
}
