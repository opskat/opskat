package policy_group_entity

import (
	"encoding/json"
	"testing"

	"github.com/opskat/opskat/internal/model/entity/policy"
	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
)

func TestPolicyGroup_Validate(t *testing.T) {
	convey.Convey("权限组校验", t, func() {
		convey.Convey("名称为空时应返回错误", func() {
			pg := &PolicyGroup{PolicyType: PolicyTypeCommand}
			err := pg.Validate()
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "名称")
		})

		convey.Convey("无效的策略类型应返回错误", func() {
			pg := &PolicyGroup{Name: "test", PolicyType: "unknown"}
			err := pg.Validate()
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "策略类型")
		})

		convey.Convey("command类型校验通过", func() {
			pg := &PolicyGroup{Name: "test", PolicyType: PolicyTypeCommand}
			err := pg.Validate()
			assert.NoError(t, err)
		})

		convey.Convey("query类型校验通过", func() {
			pg := &PolicyGroup{Name: "test", PolicyType: PolicyTypeQuery}
			err := pg.Validate()
			assert.NoError(t, err)
		})

		convey.Convey("redis类型校验通过", func() {
			pg := &PolicyGroup{Name: "test", PolicyType: PolicyTypeRedis}
			err := pg.Validate()
			assert.NoError(t, err)
		})
	})
}

func TestIsBuiltinKind(t *testing.T) {
	convey.Convey("isBuiltinKind 从注册数据派生合法 kind", t, func() {
		convey.Convey("已注册的 6 个内置 kind 均为真", func() {
			for _, k := range []string{
				PolicyTypeCommand, PolicyTypeQuery, PolicyTypeRedis,
				PolicyTypeMongo, PolicyTypeKafka, PolicyTypeEtcd,
			} {
				assert.True(t, isBuiltinKind(k), "kind %s 应为已注册内置 kind", k)
			}
		})
		convey.Convey("未注册 kind 为假", func() {
			assert.False(t, isBuiltinKind("unknown"))
			assert.False(t, isBuiltinKind(""))
		})
	})
}

func TestBuiltinGroups_DerivesOrderFromRegistration(t *testing.T) {
	// 守护:经 registerBuiltinGroups 注册的 kind 必须自动出现在 BuiltinGroups(),
	// 不依赖手维护的 builtinKindOrder —— 否则新增 kind 只注册数据却漏改顺序表时,
	// 其内置组会被静默丢弃(isBuiltinKind/Validate 放行,但 BuiltinGroups 不返回、
	// builtinMap 缓存不到、FindBuiltin 取 nil),与"一处注册"的 OCP 目标相悖。
	const testKind = "__synthetic_test_kind__"

	origOrder := builtinKindOrder
	t.Cleanup(func() {
		delete(builtinGroupsByKind, testKind)
		builtinKindOrder = origOrder
	})

	registerBuiltinGroups(testKind, &PolicyGroup{
		BuiltinID:  policy.BuiltinPrefix + "synthetic-test",
		Name:       "synthetic",
		PolicyType: testKind,
		Policy:     "{}",
	})

	assert.True(t, isBuiltinKind(testKind), "注册后应被 isBuiltinKind 认可")

	var found bool
	for _, g := range BuiltinGroups() {
		if g.PolicyType == testKind {
			found = true
			break
		}
	}
	assert.True(t, found, "经 registerBuiltinGroups 注册的 kind 应出现在 BuiltinGroups(),不应被顺序表静默丢弃")
}

func TestPolicyGroup_ToItem(t *testing.T) {
	convey.Convey("ToItem转换", t, func() {
		convey.Convey("内置组转换后ID为字符串且Builtin为true", func() {
			pg := &PolicyGroup{
				BuiltinID:   policy.BuiltinLinuxReadOnly,
				Name:        "Linux Read-Only",
				Description: "Common Linux read-only commands",
				PolicyType:  PolicyTypeCommand,
				Policy:      `{}`,
			}
			item := pg.ToItem()
			assert.Equal(t, policy.BuiltinLinuxReadOnly, item.ID)
			assert.Equal(t, pg.Name, item.Name)
			assert.Equal(t, pg.Description, item.Description)
			assert.Equal(t, pg.PolicyType, item.PolicyType)
			assert.Equal(t, pg.Policy, item.Policy)
			assert.True(t, item.Builtin)
		})

		convey.Convey("用户组转换后ID为数字字符串且Builtin为false", func() {
			pg := &PolicyGroup{
				ID:         1,
				Name:       "用户组",
				PolicyType: PolicyTypeQuery,
				Policy:     `{}`,
			}
			item := pg.ToItem()
			assert.False(t, item.Builtin)
			assert.Equal(t, "1", item.ID)
		})
	})
}

func TestIsBuiltinID(t *testing.T) {
	convey.Convey("IsBuiltinID检查", t, func() {
		convey.Convey("builtin:前缀为内置", func() {
			assert.True(t, IsBuiltinID("builtin:linux-readonly"))
			assert.True(t, IsBuiltinID("builtin:k8s-readonly"))
		})

		convey.Convey("空字符串不是内置", func() {
			assert.False(t, IsBuiltinID(""))
		})

		convey.Convey("数字字符串不是内置", func() {
			assert.False(t, IsBuiltinID("1"))
			assert.False(t, IsBuiltinID("100"))
		})
	})
}

func TestFindBuiltin(t *testing.T) {
	convey.Convey("FindBuiltin查找内置权限组", t, func() {
		convey.Convey("按已知ID查找应返回对应内置组", func() {
			pg := FindBuiltin(policy.BuiltinLinuxReadOnly)
			assert.NotNil(t, pg)
			assert.Equal(t, policy.BuiltinLinuxReadOnly, pg.BuiltinID)
			assert.Equal(t, PolicyTypeCommand, pg.PolicyType)
		})

		convey.Convey("按不存在的ID查找应返回nil", func() {
			pg := FindBuiltin("builtin:nonexistent")
			assert.Nil(t, pg)
		})

		convey.Convey("数字字符串查找应返回nil", func() {
			pg := FindBuiltin("1")
			assert.Nil(t, pg)
		})
	})
}

func TestBuiltinGroups(t *testing.T) {
	convey.Convey("BuiltinGroups内置权限组列表", t, func() {
		groups := BuiltinGroups()
		counts := countBuiltinGroupsByPolicyType(groups)

		convey.Convey("共返回21个内置组", func() {
			assert.Len(t, groups, 21)
		})

		convey.Convey("所有内置组ID均以builtin:开头", func() {
			for _, g := range groups {
				assert.True(t, IsBuiltinID(g.BuiltinID), "内置组ID应以builtin:开头，实际ID=%s", g.BuiltinID)
			}
		})

		convey.Convey("command类型内置组有5个", func() {
			assert.Equal(t, 5, counts[PolicyTypeCommand])
		})

		convey.Convey("query类型内置组有2个", func() {
			assert.Equal(t, 2, counts[PolicyTypeQuery])
		})

		convey.Convey("redis类型内置组有2个", func() {
			assert.Equal(t, 2, counts[PolicyTypeRedis])
		})

		convey.Convey("mongo类型内置组有3个", func() {
			assert.Equal(t, 3, counts[PolicyTypeMongo])
		})

		convey.Convey("kafka类型内置组有7个", func() {
			assert.Equal(t, 7, counts[PolicyTypeKafka])
			assert.NotNil(t, FindBuiltin(policy.BuiltinKafkaMetadataReadOnly))
			assert.NotNil(t, FindBuiltin(policy.BuiltinKafkaDangerousDeny))
		})

		convey.Convey("etcd类型内置组有2个", func() {
			assert.Equal(t, 2, counts[PolicyTypeEtcd])
		})
	})
}

func countBuiltinGroupsByPolicyType(groups []*PolicyGroup) map[string]int {
	counts := make(map[string]int)
	for _, g := range groups {
		counts[g.PolicyType]++
	}
	return counts
}

func TestBuiltinGroups_Etcd(t *testing.T) {
	groups := BuiltinGroups()
	var readOnly, deny *PolicyGroup
	for _, g := range groups {
		switch g.BuiltinID {
		case policy.BuiltinEtcdReadOnly:
			readOnly = g
		case policy.BuiltinEtcdDangerousDeny:
			deny = g
		}
	}
	assert.NotNil(t, readOnly, "etcd read-only builtin group missing")
	assert.NotNil(t, deny, "etcd dangerous-deny builtin group missing")
	assert.Equal(t, PolicyTypeEtcd, readOnly.PolicyType)
	assert.Equal(t, PolicyTypeEtcd, deny.PolicyType)

	var pRO policy.EtcdPolicy
	assert.NoError(t, json.Unmarshal([]byte(readOnly.Policy), &pRO))
	assert.Contains(t, pRO.AllowList, "get *")

	var pDeny policy.EtcdPolicy
	assert.NoError(t, json.Unmarshal([]byte(deny.Policy), &pDeny))
	assert.Contains(t, pDeny.DenyList, "member remove *")
}

func TestPolicyGroup_ValidateEtcdType(t *testing.T) {
	pg := &PolicyGroup{Name: "test etcd group", PolicyType: PolicyTypeEtcd, Policy: "{}"}
	assert.NoError(t, pg.Validate())
}

func TestPolicyGroup_FindBuiltinEtcd(t *testing.T) {
	assert.NotNil(t, FindBuiltin(policy.BuiltinEtcdReadOnly))
	assert.NotNil(t, FindBuiltin(policy.BuiltinEtcdDangerousDeny))
}
