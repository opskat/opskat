package connpool

import (
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
)

// --- 测试桩 ---

type fakeClient struct {
	id     int64
	closed atomic.Bool
}

func (f *fakeClient) Close() error {
	if f.closed.Swap(true) {
		return errors.New("already closed")
	}
	return nil
}

type fakeCloser struct {
	closed atomic.Bool
}

func (f *fakeCloser) Close() error {
	f.closed.Store(true)
	return nil
}

// --- 测试 ---

func TestAssetConnCache_GetOrDial_CachesAndReuses(t *testing.T) {
	Convey("GetOrDial 命中缓存时不重复 dial", t, func() {
		c := NewAssetConnCache[*fakeClient]("test", time.Minute)
		defer func() { _ = c.Close() }()

		var dialCount atomic.Int32
		dial := func() (*fakeClient, io.Closer, error) {
			dialCount.Add(1)
			return &fakeClient{id: 1}, &fakeCloser{}, nil
		}

		client1, _, err := c.GetOrDial(1, "1:db", dial)
		So(err, ShouldBeNil)
		So(client1, ShouldNotBeNil)
		So(dialCount.Load(), ShouldEqual, 1)

		client2, _, err := c.GetOrDial(1, "1:db", dial)
		So(err, ShouldBeNil)
		So(client2, ShouldEqual, client1)
		So(dialCount.Load(), ShouldEqual, 1)
	})
}
func TestAssetConnCache_GetOrDial_ConcurrentSameKeyDialsOnce(t *testing.T) {
	Convey("同 key 并发首拨经 singleflight 合流为一次", t, func() {
		c := NewAssetConnCache[*fakeClient]("test", time.Minute)
		defer func() { _ = c.Close() }()

		var dialCount atomic.Int32
		var dialErr error
		start := make(chan struct{})
		dial := func() (*fakeClient, io.Closer, error) {
			dialCount.Add(1)
			time.Sleep(20 * time.Millisecond)
			return &fakeClient{id: 1}, &fakeCloser{}, dialErr
		}

		var wg sync.WaitGroup
		var firstClient *fakeClient
		var mu sync.Mutex
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				client, _, err := c.GetOrDial(1, "1:db", dial)
				assert.NoError(t, err)
				mu.Lock()
				if firstClient == nil {
					firstClient = client
				} else {
					assert.Equal(t, firstClient, client)
				}
				mu.Unlock()
			}()
		}
		close(start)
		wg.Wait()
		So(dialCount.Load(), ShouldEqual, 1)
	})
}

func TestAssetConnCache_DifferentKeysIndependent(t *testing.T) {
	Convey("不同 key 独立缓存", t, func() {
		c := NewAssetConnCache[*fakeClient]("test", time.Minute)
		defer func() { _ = c.Close() }()

		dialA := func() (*fakeClient, io.Closer, error) {
			return &fakeClient{id: 1}, nil, nil
		}
		dialB := func() (*fakeClient, io.Closer, error) {
			return &fakeClient{id: 2}, nil, nil
		}

		a, _, _ := c.GetOrDial(1, "1:db1", dialA)
		b, _, _ := c.GetOrDial(2, "2:db2", dialB)
		So(a.id, ShouldEqual, 1)
		So(b.id, ShouldEqual, 2)
		So(a, ShouldNotEqual, b)
	})
}

func TestAssetConnCache_DialError_NotCached(t *testing.T) {
	Convey("dial 失败时不进缓存,下次重试", t, func() {
		c := NewAssetConnCache[*fakeClient]("test", time.Minute)
		defer func() { _ = c.Close() }()

		var attempts atomic.Int32
		dial := func() (*fakeClient, io.Closer, error) {
			n := attempts.Add(1)
			if n == 1 {
				return nil, nil, errors.New("boom")
			}
			return &fakeClient{id: 1}, nil, nil
		}

		_, _, err := c.GetOrDial(1, "1:db", dial)
		So(err, ShouldNotBeNil)

		client, _, err := c.GetOrDial(1, "1:db", dial)
		So(err, ShouldBeNil)
		So(client, ShouldNotBeNil)
		So(attempts.Load(), ShouldEqual, 2)
	})
}

func TestAssetConnCache_Drop_ClosesAndRemoves(t *testing.T) {
	Convey("Drop 关闭并移除指定 key", t, func() {
		c := NewAssetConnCache[*fakeClient]("test", time.Minute)
		defer func() { _ = c.Close() }()

		client := &fakeClient{id: 1}
		tunnel := &fakeCloser{}
		dial := func() (*fakeClient, io.Closer, error) {
			return client, tunnel, nil
		}
		_, _, err := c.GetOrDial(1, "1:db", dial)
		So(err, ShouldBeNil)

		c.Drop("1:db")
		So(client.closed.Load(), ShouldBeTrue)
		So(tunnel.closed.Load(), ShouldBeTrue)

		// 下次 GetOrDial 应该重新 dial
		newClient := &fakeClient{id: 2}
		dial2 := func() (*fakeClient, io.Closer, error) {
			return newClient, nil, nil
		}
		got, _, err := c.GetOrDial(1, "1:db", dial2)
		So(err, ShouldBeNil)
		So(got, ShouldEqual, newClient)
	})
}

func TestAssetConnCache_Close_ReleasesAll(t *testing.T) {
	Convey("Close 释放所有缓存的 client 和 tunnel", t, func() {
		c := NewAssetConnCache[*fakeClient]("test", time.Minute)

		clientA := &fakeClient{id: 1}
		clientB := &fakeClient{id: 2}
		tunnelA := &fakeCloser{}
		tunnelB := &fakeCloser{}

		_, _, _ = c.GetOrDial(1, "1:db", func() (*fakeClient, io.Closer, error) {
			return clientA, tunnelA, nil
		})
		_, _, _ = c.GetOrDial(2, "2:db", func() (*fakeClient, io.Closer, error) {
			return clientB, tunnelB, nil
		})

		err := c.Close()
		So(err, ShouldBeNil)
		So(clientA.closed.Load(), ShouldBeTrue)
		So(clientB.closed.Load(), ShouldBeTrue)
		So(tunnelA.closed.Load(), ShouldBeTrue)
		So(tunnelB.closed.Load(), ShouldBeTrue)
	})
}

func TestAssetConnCache_Evictor_EvictsIdle(t *testing.T) {
	Convey("idle 超过 TTL 后被 evictor 驱逐", t, func() {
		c := NewAssetConnCache[*fakeClient]("test", 50*time.Millisecond)
		defer func() { _ = c.Close() }()

		client := &fakeClient{id: 1}
		_, _, _ = c.GetOrDial(1, "1:db", func() (*fakeClient, io.Closer, error) {
			return client, nil, nil
		})

		// 触发一次驱逐扫描,模拟时间过去
		c.EvictIdle(time.Now().Add(time.Second))

		So(client.closed.Load(), ShouldBeTrue)

		// 缓存里应该已经没有这个 key 了
		So(c.size(), ShouldEqual, 0)
	})
}

func TestAssetConnCache_Hold_KeepsAnEntryInUseFromIdleEviction(t *testing.T) {
	Convey("被占用的连接不算空闲", t, func() {
		c := NewAssetConnCache[*fakeClient]("test", 50*time.Millisecond)
		defer func() { _ = c.Close() }()
		client := &fakeClient{id: 1}
		_, _, _ = c.GetOrDial(1, "1:db", func() (*fakeClient, io.Closer, error) {
			return client, nil, nil
		})
		later := func() time.Time { return time.Now().Add(time.Second) }

		Convey("两次占用都放开之前不被驱逐,重复放开同一次占用不抵掉另一次", func() {
			releaseFirst := c.Hold("1:db")
			releaseSecond := c.Hold("1:db")

			c.EvictIdle(later())
			So(client.closed.Load(), ShouldBeFalse)

			releaseFirst()
			releaseFirst()
			c.EvictIdle(later())
			So(client.closed.Load(), ShouldBeFalse)

			releaseSecond()
			c.EvictIdle(later())
			So(client.closed.Load(), ShouldBeTrue)
			So(c.size(), ShouldEqual, 0)
		})

		Convey("显式失效不看占用", func() {
			release := c.Hold("1:db")
			c.DropAsset(1)
			So(client.closed.Load(), ShouldBeTrue)
			release()
		})

		Convey("key 不在缓存里时放开是空操作", func() {
			c.Hold("9:missing")()
			So(c.size(), ShouldEqual, 1)
		})
	})
}

func TestAssetConnCache_DropAsset(t *testing.T) {
	Convey("DropAsset 关掉该资产的全部连接,不误伤其它资产", t, func() {
		c := NewAssetConnCache[*fakeClient]("test", time.Minute)
		defer func() { _ = c.Close() }()

		dial := func(id int64) func() (*fakeClient, io.Closer, error) {
			return func() (*fakeClient, io.Closer, error) { return &fakeClient{id: id}, nil, nil }
		}
		// key 前缀相同但资产不同（1 vs 12）：靠字符串前缀匹配会误伤，靠 assetID 不会。
		a1, _, _ := c.GetOrDial(1, "1:db1", dial(1))
		a2, _, _ := c.GetOrDial(1, "1:db2", dial(2))
		other, _, _ := c.GetOrDial(12, "12:db1", dial(3))

		c.DropAsset(1)

		So(a1.closed.Load(), ShouldBeTrue)
		So(a2.closed.Load(), ShouldBeTrue)
		So(other.closed.Load(), ShouldBeFalse)
		So(c.size(), ShouldEqual, 1)
	})
}

// 拨号进行中资产被改配置（DropAsset）或缓存被关闭（Close）：这次拨出的连接按旧配置建立，
// 不得在失效之后才落进缓存，否则之后的操作一直复用它（轮询中的面板永远不会空闲驱逐）。
func TestAssetConnCache_InvalidatedDuringDialIsNotCached(t *testing.T) {
	invalidations := map[string]func(c *AssetConnCache[*fakeClient]){
		"DropAsset": func(c *AssetConnCache[*fakeClient]) { c.DropAsset(1) },
		"Close":     func(c *AssetConnCache[*fakeClient]) { _ = c.Close() },
	}
	for name, invalidate := range invalidations {
		t.Run(name, func(t *testing.T) {
			c := NewAssetConnCache[*fakeClient]("test", time.Minute)
			defer func() { _ = c.Close() }()

			dialing, finish := make(chan struct{}), make(chan struct{})
			stale, tunnel := &fakeClient{id: 1}, &fakeCloser{}
			type result struct {
				client *fakeClient
				err    error
			}
			got := make(chan result, 1)
			go func() {
				client, _, err := c.GetOrDial(1, "1:db", func() (*fakeClient, io.Closer, error) {
					close(dialing)
					<-finish
					return stale, tunnel, nil
				})
				got <- result{client, err}
			}()
			<-dialing
			invalidate(c)
			close(finish)
			r := <-got

			assert.ErrorIs(t, r.err, ErrConnInvalidated)
			assert.Nil(t, r.client)
			assert.True(t, stale.closed.Load(), "the stale client must be closed")
			assert.True(t, tunnel.closed.Load(), "the stale tunnel must be closed")
			assert.Equal(t, 0, c.size())

			fresh := &fakeClient{id: 2}
			next, _, err := c.GetOrDial(1, "1:db", func() (*fakeClient, io.Closer, error) { return fresh, nil, nil })
			assert.NoError(t, err)
			assert.Same(t, fresh, next, "the next use dials again")
		})
	}
}

// 别的资产失效不影响进行中的拨号。
func TestAssetConnCache_OtherAssetInvalidationDoesNotDiscardDial(t *testing.T) {
	c := NewAssetConnCache[*fakeClient]("test", time.Minute)
	defer func() { _ = c.Close() }()

	dialing, finish := make(chan struct{}), make(chan struct{})
	client := &fakeClient{id: 1}
	errc := make(chan error, 1)
	go func() {
		_, _, err := c.GetOrDial(1, "1:db", func() (*fakeClient, io.Closer, error) {
			close(dialing)
			<-finish
			return client, nil, nil
		})
		errc <- err
	}()
	<-dialing
	c.DropAsset(2)
	close(finish)

	assert.NoError(t, <-errc)
	assert.False(t, client.closed.Load())
	assert.Equal(t, 1, c.size())
}
