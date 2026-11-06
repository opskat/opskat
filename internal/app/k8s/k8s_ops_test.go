package k8s

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"sync"
	"testing"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSelectDialSource(t *testing.T) {
	proxyCfg := &asset_entity.K8sConfig{Proxy: &asset_entity.ProxyConfig{Type: "socks5", Host: "p", Port: 1080}}
	noProxy := &asset_entity.K8sConfig{}

	cases := []struct {
		name          string
		tunnelID      int64
		cfg           *asset_entity.K8sConfig
		poolAvailable bool
		want          dialSource
	}{
		{"proxy only", 0, proxyCfg, true, dialProxy},
		{"tunnel only", 5, noProxy, true, dialTunnel},
		{"tunnel preferred over proxy", 5, proxyCfg, true, dialTunnel},
		{"direct (neither)", 0, noProxy, true, dialNone},
		{"tunnel set but pool unavailable", 5, noProxy, false, dialNone},
		{"tunnel set + proxy but pool unavailable (tunnel still claims the slot)", 5, proxyCfg, false, dialNone},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := &asset_entity.Asset{SSHTunnelID: c.tunnelID}
			assert.Equal(t, c.want, selectDialSource(a, c.cfg, c.poolAvailable))
		})
	}
}

func TestK8sClientOptionsRejectsInvalidProxyChain(t *testing.T) {
	k := &K8s{}
	enabled := true
	_, err := k.k8sClientOptions(context.Background(), &asset_entity.Asset{}, &asset_entity.K8sConfig{
		ProxyChain: &asset_entity.ProxyChainConfig{Layers: []asset_entity.ProxyChainLayer{{Type: asset_entity.ProxyChainLayerSOCKS5, Enabled: &enabled, Port: 1080}}},
	})
	require.ErrorContains(t, err, "SOCKS5")
}

// chunkReader 每次 Read 只吐出一个小块，块读完后返回 err（nil 视为 io.EOF）。
type chunkReader struct {
	chunks [][]byte
	err    error
}

func (r *chunkReader) Read(p []byte) (int, error) {
	if len(r.chunks) == 0 {
		if r.err != nil {
			return 0, r.err
		}
		return 0, io.EOF
	}
	n := copy(p, r.chunks[0])
	r.chunks = r.chunks[1:]
	return n, nil
}

type emitted struct {
	event   string
	payload any
}

func TestPumpPodLogsBatchesSmallReadsAndEndsAfterTail(t *testing.T) {
	for name, streamErr := range map[string]error{"eof": nil, "stream error": errors.New("stream reset")} {
		t.Run(name, func(t *testing.T) {
			const n = 500
			var want bytes.Buffer
			r := &chunkReader{err: streamErr}
			for i := 0; i < n; i++ {
				line := []byte(fmt.Sprintf("log line %03d\n", i))
				want.Write(line)
				r.chunks = append(r.chunks, line)
			}

			var mu sync.Mutex
			var events []emitted
			pumpPodLogs(r, "s1", func(event string, payload any) {
				mu.Lock()
				defer mu.Unlock()
				events = append(events, emitted{event, payload})
			})
			mu.Lock()
			defer mu.Unlock()

			var got bytes.Buffer
			dataEvents := 0
			for _, e := range events {
				if e.event == "k8s:log:s1" {
					dataEvents++
					raw, err := base64.StdEncoding.DecodeString(e.payload.(string))
					require.NoError(t, err)
					got.Write(raw)
				}
			}
			assert.Equal(t, want.String(), got.String(), "decoded log bytes must be identical and in order")
			t.Logf("k8s:log events for %d small reads: %d", n, dataEvents)
			assert.Less(t, dataEvents, n/10, "small log reads must be coalesced, not emitted one by one")

			// 尾部数据先于 logerr / logend；logend 永远是最后一个事件。
			require.GreaterOrEqual(t, len(events), 3)
			assert.Equal(t, emitted{"k8s:logend:s1", "s1"}, events[len(events)-1])
			if streamErr != nil {
				assert.Equal(t, emitted{"k8s:logerr:s1", "stream reset"}, events[len(events)-2])
				assert.Equal(t, "k8s:log:s1", events[len(events)-3].event)
			} else {
				assert.Equal(t, "k8s:log:s1", events[len(events)-2].event)
			}
		})
	}
}
