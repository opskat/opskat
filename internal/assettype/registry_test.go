package assettype

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	policyent "github.com/opskat/opskat/internal/model/entity/policy"
	"github.com/smartystreets/goconvey/convey"
)

type stubHandler struct {
	typ  string
	port int
}

func (s *stubHandler) Type() string     { return s.typ }
func (s *stubHandler) DefaultPort() int { return s.port }
func (s *stubHandler) SafeView(_ *asset_entity.Asset) map[string]any {
	return map[string]any{"stub": true}
}
func (s *stubHandler) ResolvePassword(_ context.Context, _ *asset_entity.Asset) (string, error) {
	return "", nil
}
func (s *stubHandler) DefaultPolicy() any { return nil }
func (s *stubHandler) PolicyKind() string { return "" }
func (s *stubHandler) AutomationContract() AutomationContract {
	return newAutomationContract([]string{"value"}, []string{"value"}, nil, nil, nil)
}
func (s *stubHandler) ValidateCreateArgs(_ map[string]any) error { return nil }
func (s *stubHandler) ApplyCreateArgs(_ context.Context, _ *asset_entity.Asset, _ map[string]any) error {
	return nil
}
func (s *stubHandler) ApplyUpdateArgs(_ context.Context, _ *asset_entity.Asset, _ map[string]any) error {
	return nil
}

func TestRegistry(t *testing.T) {
	convey.Convey("AssetType Registry", t, func() {
		mu.Lock()
		orig := registry
		registry = map[string]AssetTypeHandler{}
		mu.Unlock()
		defer func() {
			mu.Lock()
			registry = orig
			mu.Unlock()
		}()

		convey.Convey("Get returns false for unregistered type", func() {
			_, ok := Get("nonexistent")
			convey.So(ok, convey.ShouldBeFalse)
		})

		convey.Convey("Register and Get works", func() {
			Register(&stubHandler{typ: "test", port: 9999})
			h, ok := Get("test")
			convey.So(ok, convey.ShouldBeTrue)
			convey.So(h.Type(), convey.ShouldEqual, "test")
			convey.So(h.DefaultPort(), convey.ShouldEqual, 9999)
		})

		convey.Convey("All returns handlers in stable type order", func() {
			Register(&stubHandler{typ: "b", port: 2})
			Register(&stubHandler{typ: "a", port: 1})
			handlers := All()
			convey.So(len(handlers), convey.ShouldEqual, 2)
			convey.So(handlers[0].Type(), convey.ShouldEqual, "a")
			convey.So(handlers[1].Type(), convey.ShouldEqual, "b")
		})
	})
}

func TestHandlerPolicyKind(t *testing.T) {
	convey.Convey("内置 handler 声明 policyKind 并接线到注册表", t, func() {
		want := map[string]string{
			asset_entity.AssetTypeSSH:      policyent.PolicyKindCommand,
			asset_entity.AssetTypeSerial:   policyent.PolicyKindCommand,
			asset_entity.AssetTypeLocal:    policyent.PolicyKindCommand,
			asset_entity.AssetTypeDatabase: policyent.PolicyKindQuery,
			asset_entity.AssetTypeRedis:    policyent.PolicyKindRedis,
			asset_entity.AssetTypeMongoDB:  policyent.PolicyKindMongo,
			asset_entity.AssetTypeKafka:    policyent.PolicyKindKafka,
			asset_entity.AssetTypeK8s:      policyent.PolicyKindK8s,
			asset_entity.AssetTypeEtcd:     policyent.PolicyKindEtcd,
		}
		for typ, kind := range want {
			h, ok := Get(typ)
			convey.So(ok, convey.ShouldBeTrue)
			convey.So(h.PolicyKind(), convey.ShouldEqual, kind)
			got, ok := policyent.AssetKindOf(typ)
			convey.So(ok, convey.ShouldBeTrue)
			convey.So(got, convey.ShouldEqual, kind)
		}
	})
}

func TestRegisterSkipsEmptyKind(t *testing.T) {
	convey.Convey("PolicyKind 为空的 handler 不污染 asset-kind 注册表", t, func() {
		Register(&stubHandler{typ: "emptykindstub"})
		defer func() {
			mu.Lock()
			delete(registry, "emptykindstub")
			mu.Unlock()
		}()
		_, ok := policyent.AssetKindOf("emptykindstub")
		convey.So(ok, convey.ShouldBeFalse)
	})
}

func TestRegisteredHandlersOwnAuthenticationAssociationProjection(t *testing.T) {
	tests := []struct {
		name     string
		asset    *asset_entity.Asset
		want     AuthenticationAssociation
		wantAuth bool
	}{
		{
			name: "SSH managed key",
			asset: func() *asset_entity.Asset {
				a := &asset_entity.Asset{Type: asset_entity.AssetTypeSSH}
				require.NoError(t, a.SetSSHConfig(&asset_entity.SSHConfig{AuthType: asset_entity.AuthTypeKey, CredentialID: 7}))
				return a
			}(),
			want:     AuthenticationAssociation{Type: "ssh_key", Ref: "credential:7"},
			wantAuth: true,
		},
		{
			name: "SSH agent",
			asset: func() *asset_entity.Asset {
				a := &asset_entity.Asset{Type: asset_entity.AssetTypeSSH}
				require.NoError(t, a.SetSSHConfig(&asset_entity.SSHConfig{AuthType: asset_entity.AuthTypeAgent, AgentSourceID: 4, AgentKeyFingerprint: "SHA256:selected"}))
				return a
			}(),
			want:     AuthenticationAssociation{Type: "ssh_agent", Ref: "agent-source:4", Fingerprint: "SHA256:selected"},
			wantAuth: true,
		},
		{
			name: "database password",
			asset: func() *asset_entity.Asset {
				a := &asset_entity.Asset{Type: asset_entity.AssetTypeDatabase}
				require.NoError(t, a.SetDatabaseConfig(&asset_entity.DatabaseConfig{Driver: asset_entity.DriverMySQL, CredentialID: 8}))
				return a
			}(),
			want:     AuthenticationAssociation{Type: "password", Ref: "credential:8"},
			wantAuth: true,
		},
		{
			name: "legacy inline password",
			asset: func() *asset_entity.Asset {
				a := &asset_entity.Asset{Type: asset_entity.AssetTypeRedis}
				require.NoError(t, a.SetRedisConfig(&asset_entity.RedisConfig{Password: "cipher-inline"}))
				return a
			}(),
			wantAuth: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, ok := Get(tc.asset.Type)
			require.True(t, ok)
			got, ok, err := AuthenticationAssociationOf(h, tc.asset)
			require.NoError(t, err)
			assert.Equal(t, tc.wantAuth, ok)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestRDPHasNoPolicy(t *testing.T) {
	convey.Convey("RDP 只建立远程桌面会话，不声明命令策略或权限组", t, func() {
		h, ok := Get(asset_entity.AssetTypeRDP)
		convey.So(ok, convey.ShouldBeTrue)
		convey.So(h.DefaultPolicy(), convey.ShouldBeNil)
		convey.So(h.PolicyKind(), convey.ShouldBeEmpty)

		_, ok = policyent.AssetKindOf(asset_entity.AssetTypeRDP)
		convey.So(ok, convey.ShouldBeFalse)
		_, ok = policyent.GetDefaultPolicyOf(asset_entity.AssetTypeRDP)
		convey.So(ok, convey.ShouldBeFalse)
	})
}

// TestArgStringIsStrictlyTyped 钉住 ArgString 的严格契约：只有真正的 string 才返回其值，
// 其它类型（map/slice/数字/布尔/nil/缺失）一律返回空串，绝不用 fmt.Sprintf 把复合值
// 字符串化——那会让藏了嵌套 secret 的复合 host/username 混过“必填”校验。
func TestArgStringIsStrictlyTyped(t *testing.T) {
	assert.Equal(t, "box", ArgString(map[string]any{"host": "box"}, "host"))
	assert.Equal(t, "", ArgString(map[string]any{"host": ""}, "host"))
	assert.Equal(t, "", ArgString(map[string]any{"host": map[string]any{"password": "s"}}, "host"))
	assert.Equal(t, "", ArgString(map[string]any{"host": []any{"a"}}, "host"))
	assert.Equal(t, "", ArgString(map[string]any{"host": 42}, "host"))
	assert.Equal(t, "", ArgString(map[string]any{"host": true}, "host"))
	assert.Equal(t, "", ArgString(map[string]any{"host": nil}, "host"))
	assert.Equal(t, "", ArgString(map[string]any{}, "host"))
}

// TestArgStringSliceRejectsNonStringItems 钉住 ArgStringSlice 的严格契约：[]string 与
// []any 全字符串项按原样保留并 trim/丢弃空项；[]any 含任一非字符串项（嵌套 map/数字/布尔/
// 切片）整体拒绝返回 nil，绝不用 fmt.Sprintf 把项字符串化——那会让藏了嵌套 secret 的
// brokers/endpoints 项混过“必填数组”校验。
func TestArgStringSliceRejectsNonStringItems(t *testing.T) {
	assert.Equal(t, []string{"a", "b"}, ArgStringSlice(map[string]any{"x": []string{"a", "b"}}, "x"))
	assert.Equal(t, []string{"a", "b"}, ArgStringSlice(map[string]any{"x": []any{"a", "b"}}, "x"))
	assert.Nil(t, ArgStringSlice(map[string]any{"x": []any{map[string]any{"password": "s"}}}, "x"))
	assert.Nil(t, ArgStringSlice(map[string]any{"x": []any{"a", 42}}, "x"))
	assert.Nil(t, ArgStringSlice(map[string]any{"x": []any{true}}, "x"))
	assert.Nil(t, ArgStringSlice(map[string]any{"x": []any{[]any{"a"}}}, "x"))
	assert.Nil(t, ArgStringSlice(map[string]any{"x": 42}, "x"))
	assert.Nil(t, ArgStringSlice(map[string]any{"x": nil}, "x"))
	assert.Equal(t, []string{"a", "b"}, ArgStringSlice(map[string]any{"x": "a,b"}, "x"))
	assert.Equal(t, []string{"a"}, ArgStringSlice(map[string]any{"x": []any{" a ", " "}}, "x"))
}
