package connpool

import (
	"testing"
	"time"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/stretchr/testify/assert"
)

func TestBuildEtcdClientConfig_Defaults(t *testing.T) {
	cfg := &asset_entity.EtcdConfig{
		Endpoints: []string{"127.0.0.1:2379"},
	}
	clientCfg, err := buildEtcdClientConfig(cfg, "")
	assert.NoError(t, err)
	assert.Equal(t, []string{"127.0.0.1:2379"}, clientCfg.Endpoints)
	assert.Equal(t, 5*time.Second, clientCfg.DialTimeout)
	assert.Empty(t, clientCfg.Username)
	assert.Nil(t, clientCfg.TLS)
}

func TestBuildEtcdClientConfig_Auth(t *testing.T) {
	cfg := &asset_entity.EtcdConfig{
		Endpoints: []string{"e1:2379"},
		Username:  "root",
	}
	c, err := buildEtcdClientConfig(cfg, "s3cret")
	assert.NoError(t, err)
	assert.Equal(t, "root", c.Username)
	assert.Equal(t, "s3cret", c.Password)
}

func TestBuildEtcdClientConfig_TLSInsecure(t *testing.T) {
	cfg := &asset_entity.EtcdConfig{
		Endpoints:   []string{"e1:2379"},
		TLS:         true,
		TLSInsecure: true,
	}
	c, err := buildEtcdClientConfig(cfg, "")
	assert.NoError(t, err)
	assert.NotNil(t, c.TLS)
	assert.True(t, c.TLS.InsecureSkipVerify)
}

func TestBuildEtcdClientConfig_CustomDialTimeout(t *testing.T) {
	cfg := &asset_entity.EtcdConfig{
		Endpoints:          []string{"e1:2379"},
		DialTimeoutSeconds: 12,
	}
	c, err := buildEtcdClientConfig(cfg, "")
	assert.NoError(t, err)
	assert.Equal(t, 12*time.Second, c.DialTimeout)
}
