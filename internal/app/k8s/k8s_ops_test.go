package k8s

import (
	"testing"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/stretchr/testify/assert"
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
