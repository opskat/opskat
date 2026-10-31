package extreg

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/opskat/opskat/internal/ai/permission"
	policyent "github.com/opskat/opskat/internal/model/entity/policy"
	"github.com/opskat/opskat/internal/model/entity/policy_group_entity"
	"github.com/opskat/opskat/pkg/extension"
	"github.com/opskat/opskat/pkg/extwire"
)

var policyIDRe = regexp.MustCompile(`^ext:[a-z0-9][a-z0-9_-]{0,63}(:[a-z0-9][a-z0-9_-]{0,63})*$`)

// policyPoint 是策略面的贡献点：策略面类型、权限组与默认组的规则；接入的是策略面归属、
// 扩展权限组表、本扩展资产类型的永久规则落点与默认策略。策略动作集合不在这里声明，由工具
// 的声明推导（PoliciesDef.Actions）。
type policyPoint struct{}

func (policyPoint) Key() string { return extwire.KeyPolicy }

// Check 校验策略面。只有声明了策略动作的工具时才要求策略面类型：工具的调用按它下面的权限组
// 判定。策略面名本身是权限组 ID 与 grant 的命名空间段，与资产类型同一套命名规则。
//
// 权限组 ID 与全宿主所有扩展的共用一张表，而一个策略面只归一个已加载的扩展（接入时
// claimPolicyType 拒绝第二个认领者），所以按它命名空间化（ext:<policy.type>:<name>）才让
// "这个 ID 是我的"在加载前就能判定，而不是和恰好装着的别的扩展撞了才暴露。
func (policyPoint) Check(v extension.ExtensionView) error {
	var pol extwire.Policy
	if err := decodeContribution(v, extwire.KeyPolicy, &pol); err != nil {
		return err
	}
	var tools []extwire.Tool
	if err := decodeContribution(v, extwire.KeyTools, &tools); err != nil {
		return err
	}
	if pol.Type == "" {
		if slices.ContainsFunc(tools, func(t extwire.Tool) bool { return len(t.Actions()) > 0 }) {
			return fmt.Errorf("policy.type is required — it names the policy face this extension's tools are checked under")
		}
	} else if !nameRe.MatchString(pol.Type) {
		return fmt.Errorf("policy.type must match %s (got %q)", nameRe.String(), pol.Type)
	}
	prefix := "ext:" + pol.Type + ":"
	seen := make(map[string]struct{}, len(pol.Groups))
	for _, g := range pol.Groups {
		if !strings.HasPrefix(g.ID, prefix) || len(g.ID) == len(prefix) {
			return fmt.Errorf("policy.groups[%s]: ID must be %s<name>, namespaced by policy.type", g.ID, prefix)
		}
		if !policyIDRe.MatchString(g.ID) {
			return fmt.Errorf("policy.groups[%s]: ID has invalid characters", g.ID)
		}
		if _, dup := seen[g.ID]; dup {
			return fmt.Errorf("policy.groups[%s]: duplicate policy group ID", g.ID)
		}
		seen[g.ID] = struct{}{}
	}
	return nil
}

// Attach 认领策略面、登记权限组，并为每个资产类型登记永久规则落点与默认策略——两者都不需要
// WASM，仅描述接入同样登记：opsctl policy allow/deny/rm/show 走落点（规则形状
// `<action>[:<resource-glob>]`，按策略面落在资产 / 组各自的列上，见 rule_ext.go）；一个
// 扩展资产的默认策略只引用组 ID，组不登记 `policy show` 就解析不出它们的规则。没有策略面的
// 扩展（没有工具）什么也不接。
func (policyPoint) Attach(v extension.ExtensionView) (func(), error) {
	name := v.Name()
	m, err := ManifestOf(v.Manifest())
	if err != nil {
		return nil, err
	}
	policyType := m.Policies.Type
	if policyType == "" {
		return func() {}, nil
	}
	mu.Lock()
	err = claimPolicyType(name, policyType)
	mu.Unlock()
	if err != nil {
		return nil, err
	}
	done := undo{func() {
		mu.Lock()
		delete(policyTypeOwner, policyType)
		mu.Unlock()
	}}
	done = append(done, func() { policy_group_entity.UnregisterExtensionGroupsByExtension(name) })
	if err := registerPolicyGroups(name, m); err != nil {
		done.run()
		return nil, err
	}
	defaults := append([]string(nil), m.Policies.Default...)
	for _, at := range m.AssetTypes {
		assetType := at.Type
		if err := permission.RegisterExtensionRuleSink(assetType, policyType, m.Policies.Actions); err != nil {
			done.run()
			return nil, fmt.Errorf("policy: asset type %q: %w", assetType, err)
		}
		done = append(done, func() { permission.UnregisterRuleSink(assetType) })
		policyent.RegisterDefaultPolicy(assetType, func() any {
			return &policyent.CommandPolicy{Groups: defaults}
		})
		done = append(done, func() { policyent.UnregisterDefaultPolicy(assetType) })
	}
	return done.run, nil
}

// claimPolicyType 把 policyType 记到 extName 名下；内置策略面或已被别的扩展占用时拒绝。
// 调用方持有 mu。
func claimPolicyType(extName, policyType string) error {
	if policy_group_entity.IsBuiltinPolicyType(policyType) {
		return fmt.Errorf("policy.type %q is a built-in policy type", policyType)
	}
	if owner, taken := policyTypeOwner[policyType]; taken {
		return fmt.Errorf("policy.type %q is already owned by extension %q", policyType, owner)
	}
	policyTypeOwner[policyType] = extName
	return nil
}

// registerPolicyGroups 把扩展声明的权限组登记进内置权限组表。失败时已登记的由调用方按
// 扩展名撤掉。
func registerPolicyGroups(extName string, m *Manifest) error {
	for _, pg := range m.Policies.Groups {
		policyJSON, err := json.Marshal(pg.Policy)
		if err != nil {
			return fmt.Errorf("policy.groups[%s]: %w", pg.ID, err)
		}
		if err := policy_group_entity.RegisterExtensionGroup(&policy_group_entity.PolicyGroup{
			BuiltinID:     pg.ID,
			Name:          pg.I18n.Name,
			Description:   pg.I18n.Description,
			PolicyType:    m.Policies.Type,
			Policy:        string(policyJSON),
			ExtensionName: extName,
		}); err != nil {
			return fmt.Errorf("policy.groups[%s]: %w", pg.ID, err)
		}
	}
	return nil
}
