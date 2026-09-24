package permission

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/opskat/opskat/internal/ai/policy"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/group_entity"
	policyent "github.com/opskat/opskat/internal/model/entity/policy"
	"github.com/opskat/opskat/internal/model/entity/policy_group_entity"
	"github.com/opskat/opskat/internal/service/policy_group_svc"
)

// 永久规则（opsctl policy allow/deny）的按类型落点（spec 决策 11、15）。
//
// 两层注册，都在 type_registry.go 的 init 里与 registerPermissionType 并列：
//   - shapeLanding 按**策略列**注册（command/query/redis/...）：Get/SetXxxPolicy 对、
//     两侧列表、引用组读取、按规则移除——对资产（*asset_entity.Asset）与资产组
//     （*group_entity.Group）两种 holder 通用，两者都实现同一对访问器；
//   - ruleLanding 按**资产类型**注册：一次注册同时覆盖 allow 与 deny 两侧（两侧在每个
//     形状里本来就是成对字段——AllowList/DenyList、AllowTypes/DenyTypes），并声明
//     pattern→规则的落法与 deny 遮蔽判定用的匹配器。
//
// AppendTypeRules 只改内存里的 holder；落库（一次事务内的读-改-写）由调用方完成，
// 这样多目标/多 pattern 的全或无才能在写库前完成全部校验。

// RuleSide 是策略形状里成对出现的两个侧。
type RuleSide int

const (
	RuleAllow RuleSide = iota
	RuleDeny
)

// LandedRule 是一条请求 pattern 实际落进策略列的规则。Broader 标注"结果比请求的主体
// 更宽"（决策 12）：database 与 mongodb 的形状只表达语句/操作类型，永久化一条具体
// SQL/操作只能落成类型级规则。
type LandedRule struct {
	Rule    string
	Broader bool
}

// RuleSourceKind 是一条生效规则来自哪一层（决策 19 的来源标注）。
type RuleSourceKind int

const (
	RuleSourceAsset       RuleSourceKind = iota // 资产自身那一列
	RuleSourceGroup                             // 资产组链上的组（或组目标自身那一列）
	RuleSourcePolicyGroup                       // 策略引用的权限组（含内置组与扩展组）
)

// SourcedRule 是一条生效规则加上结构化的来源信息；CLI 负责按 locale 渲染。
type SourcedRule struct {
	Rule            string
	Kind            RuleSourceKind
	HolderID        int64  // Kind 为 Asset/Group 时的 holder ID
	HolderName      string // Kind 为 Asset/Group 时的 holder 名
	PolicyGroupID   string // Kind 为 PolicyGroup 时的权限组 ID（builtin:xxx / 数字）
	PolicyGroupName string // 权限组名（内置组含内置名）
	Generic         bool   // 来自组通用 CommandPolicy 层（非命令形状的类型也先判这层）
}

// PolicyGroupRef 是 holder 策略里引用的一个权限组。
type PolicyGroupRef struct {
	ID   string
	Name string
}

// TypeRuleView 是一个类型视角下合并生效的规则视图（show 与遮蔽检测共用）。
type TypeRuleView struct {
	Allow  []SourcedRule
	Deny   []SourcedRule
	Groups []PolicyGroupRef
}

// ruleLanding 是一个资产类型的永久规则落点。所有成员都是"该类型注册一次"的知识，
// 共享代码只经 ruleLandings 查表调用，不出现 switch assetType。
type ruleLanding struct {
	// shape 是规则落进的那一列（Get/SetXxxPolicy 对）。
	shape *shapeLanding
	// refPolicyType 是引用组按哪张权限组表解析（k8s 的 K8sPolicy 列按 command 表
	// 解析，与 collectK8sPolicies 用 ResolveCommandGroups 一致）。
	refPolicyType string
	// refShape 是解码引用权限组 Policy JSON 用的形状，注册时一次解析。内置类型的
	// refPolicyType 就是宿主的一张策略列，因此它等于 ruleShapes[refPolicyType]；
	// 扩展的权限组 kind 是 manifest 自己声明的策略面名（不是宿主的列），所以由注册
	// 方直接给出。nil 表示该落点不读引用组。
	refShape *shapeLanding
	// ownFilter 非空时，只有它认下的规则算这个类型的"holder 自身规则"。共用一列的
	// 类型才需要它：扩展全都落在 CommandPolicy 上，不过滤的话 A 扩展的 show 会把 B
	// 扩展（和 ssh）的规则列成自己的。
	ownFilter func(rule string) bool
	// land 把一条已归一化的 pattern 变成落库的规则。
	land func(pattern string) ([]LandedRule, error)
	// match 判定该形状的一条 deny 是否遮蔽一条 allow 落点（deny 无条件先判，
	// permission.go checkCommandPolicyPermission 的同一优先序）。
	match func(denyRule, rule string) bool
	// generic 非空时，该类型还先判组通用 CommandPolicy 层（CheckGroupGenericPolicy
	// 的同款匹配函数）；命令形状（ssh/serial/cp 面）的形状就是 CommandPolicy，
	// 置 nil 避免同一列读两遍。
	generic func(denyRule, rule string) bool
}

// landingMu 保护 ruleLandings。内置类型在 init() 里一次写完，扩展提供的类型则在用户
// 启用/禁用扩展时增删，与 opsctl policy / AI 会话上正在跑的读取并发（与
// type_registry.go 的 registryMu 同一理由）。shapeCanonicals 只由 init 期的
// registerRuleSink 写，运行期落点不参与认领，因此不在锁的范围里。
var landingMu sync.RWMutex

var ruleLandings = make(map[string]*ruleLanding)

// shapeCanonicals 记录每个策略列第一个注册的 canonical 资产类型。同列的所有
// canonical 共享同一个 landing.shape，归一化与落库行为完全一致，取第一个即可；
// 需要 canonical 时（如权限组按 kind 找落点）不必在 CLI/服务层维护第二张表。
var shapeCanonicals = make(map[*shapeLanding]string)

// registerRuleSink 注册一个资产类型的永久规则落点，与 registerPermissionType 的
// grantPatterns 并列（type_registry.go 的 init 里一起注册）。重复注册 panic——注册
// 冲突是启动期编程错误。
func registerRuleSink(canonical string, landing *ruleLanding) {
	// 内置类型引用的权限组 kind 就是宿主的一张策略列，解析一次存起来。
	landing.refShape = ruleShapes[landing.refPolicyType]
	if _, ok := shapeCanonicals[landing.shape]; !ok {
		shapeCanonicals[landing.shape] = canonical
	}
	if err := addRuleSink(canonical, landing); err != nil {
		panic(err.Error())
	}
}

// addRuleSink 是 init 期与运行期共用的写入：重复/非法注册报错，由调用方决定是 panic
// （内置类型的编程错误）还是拒绝加载（用户启用的扩展）。
func addRuleSink(canonical string, landing *ruleLanding) error {
	if landing.shape == nil || landing.land == nil || landing.match == nil {
		return fmt.Errorf("permission: invalid rule sink registration %q", canonical)
	}
	landingMu.Lock()
	defer landingMu.Unlock()
	if _, exists := ruleLandings[canonical]; exists {
		return fmt.Errorf("permission: duplicate rule sink registration %q", canonical)
	}
	ruleLandings[canonical] = landing
	return nil
}

func ruleLandingFor(canonicalType string) (*ruleLanding, bool) {
	landingMu.RLock()
	defer landingMu.RUnlock()
	landing, ok := ruleLandings[canonicalType]
	return landing, ok
}

// TypeRulesSupported 报告该（canonical）类型是否注册了永久规则落点。
func TypeRulesSupported(canonicalType string) bool {
	_, ok := ruleLandingFor(canonicalType)
	return ok
}

// AppendTypeRules 把已归一化的 pattern 追加到 holder 策略列的指定侧（内存读-改-写，
// 不落库）。落点为空（SQL 解析失败等）返回错误且不改动策略。
func AppendTypeRules(holder policyent.Holder, canonicalType string, side RuleSide, patterns []string) ([]LandedRule, error) {
	landing, ok := ruleLandingFor(canonicalType)
	if !ok {
		return nil, fmt.Errorf("no permanent rule support for type %q", canonicalType)
	}
	return landing.shape.appendRules(holder, side, landing.land, patterns)
}

// HolderOwnTypeRules 列出 holder 自身那一列（该类型形状）的 allow/deny 规则原文，
// 供 show 编号与 rm 定位。
func HolderOwnTypeRules(holder policyent.Holder, canonicalType string) (allow, deny []string, err error) {
	landing, ok := ruleLandingFor(canonicalType)
	if !ok {
		return nil, nil, fmt.Errorf("no permanent rule support for type %q", canonicalType)
	}
	allow, deny, _, err = landing.shape.ownSides(holder)
	return landing.ownRules(allow), landing.ownRules(deny), err
}

// ownRules 过滤出属于这个落点的 holder 自身规则（见 ruleLanding.ownFilter）。
func (l *ruleLanding) ownRules(rules []string) []string {
	if l.ownFilter == nil {
		return rules
	}
	kept := make([]string, 0, len(rules))
	for _, r := range rules {
		if l.ownFilter(r) {
			kept = append(kept, r)
		}
	}
	return kept
}

// RemoveTypeRule 从 holder 自身那一列移除一条精确匹配的规则；不存在时报错。
func RemoveTypeRule(holder policyent.Holder, canonicalType string, side RuleSide, rule string) error {
	landing, ok := ruleLandingFor(canonicalType)
	if !ok {
		return fmt.Errorf("no permanent rule support for type %q", canonicalType)
	}
	return landing.shape.removeFrom(holder, side, rule)
}

// RemoveShapeRule 从 holder 的某一形状列（policy kind）移除一条精确匹配的规则；
// 组目标的 rm 按形状定位，不经过资产类型。
func RemoveShapeRule(holder policyent.Holder, policyKind string, side RuleSide, rule string) error {
	shape, ok := ruleShapes[policyKind]
	if !ok {
		return fmt.Errorf("no permanent rule support for policy kind %q", policyKind)
	}
	return shape.removeFrom(holder, side, rule)
}

// policyRWHolder 在 policyent.Holder 的读面之上补上 Set 对——Asset 与 Group 都实现它，
// 落点的读-改-写按这一对取（spec：按 holder 取 Get/SetXxxPolicy 对，两种 holder 通用）。
type policyRWHolder interface {
	policyent.Holder
	SetCommandPolicy(*policyent.CommandPolicy) error
	SetQueryPolicy(*policyent.QueryPolicy) error
	SetRedisPolicy(*policyent.RedisPolicy) error
	SetMongoPolicy(*policyent.MongoPolicy) error
	SetKafkaPolicy(*policyent.KafkaPolicy) error
	SetK8sPolicy(*policyent.K8sPolicy) error
	SetEtcdPolicy(*policyent.EtcdPolicy) error
	SetOSSPolicy(*policyent.OSSPolicy) error
}

func asRWHolder(holder policyent.Holder) (policyRWHolder, error) {
	rw, ok := holder.(policyRWHolder)
	if !ok {
		return nil, fmt.Errorf("holder %T does not support policy writes", holder)
	}
	return rw, nil
}

type policyGroupHolder struct {
	group *policy_group_entity.PolicyGroup
}

func NewPolicyGroupHolder(group *policy_group_entity.PolicyGroup) policyent.Holder {
	return &policyGroupHolder{group: group}
}
func policyGroupPolicyOf[T any](g *policy_group_entity.PolicyGroup) (*T, error) {
	p := new(T)
	if g.Policy == "" {
		return p, nil
	}
	if err := json.Unmarshal([]byte(g.Policy), p); err != nil {
		return nil, fmt.Errorf("unmarshal policy group %q policy: %w", g.Name, err)
	}
	return p, nil
}
func (h *policyGroupHolder) set(kind string, p any) error {
	if h.group.PolicyType != kind {
		return fmt.Errorf("policy group %q has type %s; refusing to write its %s column", h.group.Name, h.group.PolicyType, kind)
	}
	b, err := json.Marshal(p)
	if err == nil {
		h.group.Policy = string(b)
	}
	return err
}
func (h *policyGroupHolder) GetCommandPolicy() (*policyent.CommandPolicy, error) {
	return policyGroupPolicyOf[policyent.CommandPolicy](h.group)
}
func (h *policyGroupHolder) GetQueryPolicy() (*policyent.QueryPolicy, error) {
	return policyGroupPolicyOf[policyent.QueryPolicy](h.group)
}
func (h *policyGroupHolder) GetRedisPolicy() (*policyent.RedisPolicy, error) {
	return policyGroupPolicyOf[policyent.RedisPolicy](h.group)
}
func (h *policyGroupHolder) GetMongoPolicy() (*policyent.MongoPolicy, error) {
	return policyGroupPolicyOf[policyent.MongoPolicy](h.group)
}
func (h *policyGroupHolder) GetKafkaPolicy() (*policyent.KafkaPolicy, error) {
	return policyGroupPolicyOf[policyent.KafkaPolicy](h.group)
}
func (h *policyGroupHolder) GetK8sPolicy() (*policyent.K8sPolicy, error) {
	return policyGroupPolicyOf[policyent.K8sPolicy](h.group)
}
func (h *policyGroupHolder) GetEtcdPolicy() (*policyent.EtcdPolicy, error) {
	return policyGroupPolicyOf[policyent.EtcdPolicy](h.group)
}
func (h *policyGroupHolder) GetOSSPolicy() (*policyent.OSSPolicy, error) {
	return policyGroupPolicyOf[policyent.OSSPolicy](h.group)
}
func (h *policyGroupHolder) SetCommandPolicy(p *policyent.CommandPolicy) error {
	return h.set(policyent.PolicyKindCommand, p)
}
func (h *policyGroupHolder) SetQueryPolicy(p *policyent.QueryPolicy) error {
	return h.set(policyent.PolicyKindQuery, p)
}
func (h *policyGroupHolder) SetRedisPolicy(p *policyent.RedisPolicy) error {
	return h.set(policyent.PolicyKindRedis, p)
}
func (h *policyGroupHolder) SetMongoPolicy(p *policyent.MongoPolicy) error {
	return h.set(policyent.PolicyKindMongo, p)
}
func (h *policyGroupHolder) SetKafkaPolicy(p *policyent.KafkaPolicy) error {
	return h.set(policyent.PolicyKindKafka, p)
}
func (h *policyGroupHolder) SetK8sPolicy(p *policyent.K8sPolicy) error {
	return h.set(policyent.PolicyKindK8s, p)
}
func (h *policyGroupHolder) SetEtcdPolicy(p *policyent.EtcdPolicy) error {
	return h.set(policyent.PolicyKindEtcd, p)
}
func (h *policyGroupHolder) SetOSSPolicy(p *policyent.OSSPolicy) error {
	return h.set(policyent.PolicyKindOSS, p)
}

// --- 形状层：一个策略列的读写 ---

// shapeSides[T] 是形状 P 的访问四件套。sides 返回两侧列表的指针，append/remove 直接改列表。
type shapeSides[T any] struct {
	get    func(policyent.Holder) (*T, error) // 读面在 Holder 上就有
	set    func(policyRWHolder, *T) error     // 写面补 Set 对
	sides  func(*T) (allow, deny, refs *[]string)
	newOne func() *T
}

// shapeLanding 是一个策略列（command/query/...）的读写落点。
type shapeLanding struct {
	appendRules func(holder policyent.Holder, side RuleSide, land func(pattern string) ([]LandedRule, error), patterns []string) ([]LandedRule, error)
	ownSides    func(holder policyent.Holder) (allow, deny, refs []string, err error)
	groupSides  func(policyJSON string) (allow, deny []string, err error)
	removeFrom  func(holder policyent.Holder, side RuleSide, rule string) error
	refs        func(holder policyent.Holder) ([]string, error)
	updateRefs  func(holder policyent.Holder, mutate func(*[]string) error) error
}

var (
	ruleShapes     = make(map[string]*shapeLanding)
	ruleShapeOrder []string
)

// registerRuleShape 注册一个策略列的读写落点（kind = policy kind 或 k8s 列）。
func registerRuleShape[T any](kind string, s shapeSides[T]) *shapeLanding {
	if _, exists := ruleShapes[kind]; exists {
		panic(fmt.Sprintf("permission: duplicate rule shape registration %q", kind))
	}
	l := &shapeLanding{
		appendRules: func(holder policyent.Holder, side RuleSide, land func(pattern string) ([]LandedRule, error), patterns []string) ([]LandedRule, error) {
			rw, err := asRWHolder(holder)
			if err != nil {
				return nil, err
			}
			p, err := s.get(rw)
			if err != nil {
				return nil, err
			}
			allowSide, denySide, _ := s.sides(p)
			sideList := allowSide
			if side == RuleDeny {
				sideList = denySide
			}
			landed := make([]LandedRule, 0, len(patterns))
			for _, pat := range patterns {
				rules, err := land(pat)
				if err != nil {
					return nil, err
				}
				landed = append(landed, rules...)
				for _, r := range rules {
					if !containsString(*sideList, r.Rule) {
						*sideList = append(*sideList, r.Rule)
					}
				}
			}
			if err := s.set(rw, p); err != nil {
				return nil, err
			}
			return landed, nil
		},
		ownSides: func(holder policyent.Holder) ([]string, []string, []string, error) {
			p, err := s.get(holder)
			if err != nil {
				return nil, nil, nil, err
			}
			allow, deny, refs := s.sides(p)
			return append([]string(nil), *allow...), append([]string(nil), *deny...), append([]string(nil), (*refs)...), nil
		},
		groupSides: func(policyJSON string) ([]string, []string, error) {
			p := s.newOne()
			if err := json.Unmarshal([]byte(policyJSON), p); err != nil {
				return nil, nil, err
			}
			allow, deny, _ := s.sides(p)
			return *allow, *deny, nil
		},
		removeFrom: func(holder policyent.Holder, side RuleSide, rule string) error {
			rw, err := asRWHolder(holder)
			if err != nil {
				return err
			}
			p, err := s.get(rw)
			if err != nil {
				return err
			}
			allowSide, denySide, _ := s.sides(p)
			sideList := allowSide
			if side == RuleDeny {
				sideList = denySide
			}
			kept := make([]string, 0, len(*sideList))
			found := false
			for _, r := range *sideList {
				if r == rule && !found {
					found = true
					continue
				}
				kept = append(kept, r)
			}
			if !found {
				return fmt.Errorf("rule %q not found", rule)
			}
			*sideList = kept
			return s.set(rw, p)
		},
		refs: func(holder policyent.Holder) ([]string, error) {
			p, err := s.get(holder)
			if err != nil {
				return nil, err
			}
			_, _, refs := s.sides(p)
			return append([]string(nil), (*refs)...), nil
		},
		updateRefs: func(holder policyent.Holder, mutate func(*[]string) error) error {
			rw, err := asRWHolder(holder)
			if err != nil {
				return err
			}
			p, err := s.get(rw)
			if err != nil {
				return err
			}
			_, _, refs := s.sides(p)
			if err := mutate(refs); err != nil {
				return err
			}
			return s.set(rw, p)
		},
	}
	ruleShapes[kind] = l
	ruleShapeOrder = append(ruleShapeOrder, kind)
	return l
}

// PolicyShapeRefs exposes the registered policy-shape seam to services which
// manage policy-group attachments. Callers do not duplicate the concrete
// policy Get/Set table.
func PolicyShapeRefs(holder policyent.Holder, policyKind string) ([]string, error) {
	shape, ok := ruleShapes[policyKind]
	if !ok {
		return nil, fmt.Errorf("no permanent rule support for policy kind %q", policyKind)
	}
	return shape.refs(holder)
}

func updatePolicyShapeRefs(holder policyent.Holder, policyKind string, mutate func(*[]string) error) error {
	shape, ok := ruleShapes[policyKind]
	if !ok {
		return fmt.Errorf("no permanent rule support for policy kind %q", policyKind)
	}
	return shape.updateRefs(holder, mutate)
}

// AddPolicyShapeRef appends one policy-group reference through the registered
// shape. Duplicate validation belongs to the service use case, which checks a
// fresh holder in its transaction before calling this narrow mutation.
func AddPolicyShapeRef(holder policyent.Holder, policyKind, ref string) error {
	return updatePolicyShapeRefs(holder, policyKind, func(refs *[]string) error {
		*refs = append(*refs, ref)
		return nil
	})
}

// RemovePolicyShapeRef removes a policy-group reference through the registered
// shape. Presence validation belongs to the service use case.
func RemovePolicyShapeRef(holder policyent.Holder, policyKind, ref string) error {
	return updatePolicyShapeRefs(holder, policyKind, func(refs *[]string) error {
		kept := (*refs)[:0]
		for _, current := range *refs {
			if current != ref {
				kept = append(kept, current)
			}
		}
		*refs = kept
		return nil
	})
}

// PolicyShapeForType returns the holder column and policy-group type accepted
// by a registered asset type. K8s writes its own column but consumes command groups.
func PolicyShapeForType(canonicalType string) (column, acceptedGroupType string, ok bool) {
	landing, ok := ruleLandingFor(canonicalType)
	if !ok {
		return "", "", false
	}
	for kind, shape := range ruleShapes {
		if shape == landing.shape {
			return kind, landing.refPolicyType, true
		}
	}
	return "", "", false
}

// CanonicalForPolicyKind 返回注册到该策略列（policy kind）的第一个 canonical 资产
// 类型。权限组只有 kind 没有资产类型，规则落点与归一化需要 canonical；同列的
// canonical 共享同一落点，取注册的第一个即可，新增形状无需在调用方维护镜像表。
func CanonicalForPolicyKind(policyKind string) (string, bool) {
	shape, ok := ruleShapes[policyKind]
	if !ok {
		return "", false
	}
	canonical, ok := shapeCanonicals[shape]
	return canonical, ok
}

func containsString(list []string, s string) bool {
	for _, item := range list {
		if item == s {
			return true
		}
	}
	return false
}

// --- pattern → 规则的落法 ---

// identityLand 原样落库：人写的通配就是他要的范围，归一化已经在
// NormalizeGrantPatterns 完成。
func identityLand(pattern string) ([]LandedRule, error) {
	pat := strings.TrimSpace(pattern)
	if pat == "" {
		return nil, errors.New("empty pattern")
	}
	return []LandedRule{{Rule: pat}}, nil
}

// queryLand 把一条 SQL pattern 落成语句类型（QueryPolicy.AllowTypes/DenyTypes 是
// 类型级语义）。每个语句落一个类型，全部标 Broader——允许一条具体 SELECT 只能落成
// "允许 SELECT 类型"（决策 12）。解析失败在写入前报错，不落一条含义不明的规则。
func queryLand(pattern string) ([]LandedRule, error) {
	stmts, err := policy.ClassifyStatements(strings.TrimSpace(pattern))
	if err != nil {
		return nil, fmt.Errorf("cannot turn %q into a statement type rule: %w", pattern, err)
	}
	landed := make([]LandedRule, 0, len(stmts))
	for _, stmt := range stmts {
		if !containsLandedRule(landed, stmt.Type) {
			landed = append(landed, LandedRule{Rule: stmt.Type, Broader: true})
		}
	}
	if len(landed) == 0 {
		return nil, errors.New("pattern produces no statement type")
	}
	return landed, nil
}

// mongoLand 把一条 mongo 命令落成 `<op> [collection]` 规则（MatchMongoRule 的规则
// 语法）：flag 不参与匹配（mongo_rule.go 的既有语义），收窄只有 collection 一维，
// 因此丢掉 flag 后的规则比带 --db/--query 的请求更宽，标 Broader。
func mongoLand(pattern string) ([]LandedRule, error) {
	fields := strings.Fields(strings.TrimSpace(pattern))
	if len(fields) == 0 || fields[0] == "" {
		return nil, errors.New("empty pattern")
	}
	rule := fields[0]
	for _, f := range fields[1:] {
		if strings.HasPrefix(f, "--") {
			continue
		}
		rule += " " + f
		break
	}
	return []LandedRule{{Rule: rule, Broader: true}}, nil
}

// cpLand 给 cp:read / cp:write 面加方向前缀：永久 cp 规则的形态是
// "cp:<dir>:<glob>"（matchCpPolicyRule 的规则语法）。
func cpLand(prefix string) func(pattern string) ([]LandedRule, error) {
	return func(pattern string) ([]LandedRule, error) {
		pat := strings.TrimSpace(pattern)
		if pat == "" {
			return nil, errors.New("empty pattern")
		}
		return []LandedRule{{Rule: prefix + pat}}, nil
	}
}

func containsLandedRule(rules []LandedRule, rule string) bool {
	for _, r := range rules {
		if r.Rule == rule {
			return true
		}
	}
	return false
}

// --- 遮蔽判定用的匹配器 ---

// queryTypeMatch 与 policy 包的 policyValueMatches（未导出）同一语义：裸 "*" 遮蔽
// 一切，否则忽略大小写与首尾空白的全等。两边必须一起改。
func queryTypeMatch(denyRule, rule string) bool {
	d := strings.TrimSpace(denyRule)
	return d == "*" || strings.EqualFold(d, strings.TrimSpace(rule))
}

// cpDenyShadows 与 checkFileTransferPermissionForDirection 的 matchCpPolicyRule
// 同一语义："cp:*" 遮蔽一切传输；同方向前缀的规则按 path glob 比较。
func cpDenyShadows(denyRule, rule string) bool {
	if denyRule == "cp:*" {
		return true
	}
	dir, denyGlob, ok := strings.Cut(denyRule, ":")
	if !ok || dir != "cp" {
		return false
	}
	dir, denyGlob, ok = strings.Cut(denyGlob, ":")
	if !ok || (dir != "read" && dir != "write") {
		return false
	}
	rdir, ruleGlob, ok := strings.Cut(rule, ":")
	if !ok || rdir != "cp" {
		return false
	}
	rdir, ruleGlob, ok = strings.Cut(ruleGlob, ":")
	if !ok || rdir != dir {
		return false
	}
	return policy.MatchPathRule(denyGlob, ruleGlob)
}

// --- 生效规则收集（show / 遮蔽检测，与 policyHoldersForAsset 判定路径同源） ---

type ruleHolderOrigin struct {
	kind   RuleSourceKind
	id     int64
	name   string
	holder policyent.Holder
}

// CollectTypeRules 收集资产 holder 链（资产 → 组链，policyHoldersForAsset 同一路径）
// 上该类型形状 + 组通用层 + 引用权限组的合并生效规则。成员判定与运行时检查同源：
// 非命令形状的类型同样先判组通用 CommandPolicy 层。
func CollectTypeRules(ctx context.Context, asset *asset_entity.Asset, canonicalType string) (*TypeRuleView, error) {
	if asset == nil {
		return nil, errors.New("no asset")
	}
	origins := []ruleHolderOrigin{{
		kind: RuleSourceAsset, id: asset.ID, name: asset.Name, holder: asset,
	}}
	for _, g := range policy.ResolveGroupChain(ctx, asset.GroupID) {
		origins = append(origins, ruleHolderOrigin{
			kind: RuleSourceGroup, id: g.ID, name: g.Name, holder: g,
		})
	}
	return collectRules(ctx, origins, canonicalType)
}

// CollectHolderTypeRules 收集单个 holder（组目标）自身那一列 + 引用权限组的规则，
// 用来核对组级写入与做组目标的遮蔽检测。
func CollectHolderTypeRules(ctx context.Context, group *group_entity.Group, canonicalType string) (*TypeRuleView, error) {
	if group == nil {
		return nil, errors.New("no group")
	}
	return collectRules(ctx, []ruleHolderOrigin{{
		kind: RuleSourceGroup, id: group.ID, name: group.Name, holder: group,
	}}, canonicalType)
}

func collectRules(ctx context.Context, origins []ruleHolderOrigin, canonicalType string) (*TypeRuleView, error) {
	landing, ok := ruleLandingFor(canonicalType)
	if !ok {
		return nil, fmt.Errorf("no permanent rule support for type %q", canonicalType)
	}
	view := &TypeRuleView{}
	seenGroups := make(map[string]bool)
	for _, o := range origins {
		allow, deny, refs, err := landing.shape.ownSides(o.holder)
		if err != nil {
			return nil, err
		}
		for _, r := range landing.ownRules(allow) {
			view.Allow = append(view.Allow, sourcedRule(o, r, false))
		}
		for _, r := range landing.ownRules(deny) {
			view.Deny = append(view.Deny, sourcedRule(o, r, false))
		}
		collectPolicyGroupRules(ctx, landing, refs, view, seenGroups)

		// 组通用层（非命令形状）：**组链**上每个组的 CmdPolicy 及其引用的 command
		// 权限组也参与判定（与 CheckGroupGenericPolicy 的输入一致——它只看组链，
		// 不看资产自身的 CmdPolicy，因此资产 origin 跳过这一层）。
		if landing.generic != nil && o.kind == RuleSourceGroup {
			cp, err := o.holder.GetCommandPolicy()
			if err != nil {
				return nil, err
			}
			for _, r := range cp.AllowList {
				view.Allow = append(view.Allow, sourcedRule(o, r, true))
			}
			for _, r := range cp.DenyList {
				view.Deny = append(view.Deny, sourcedRule(o, r, true))
			}
			collectPolicyGroupRules(ctx, genericRuleLanding, cp.Groups, view, seenGroups)
		}
	}
	return view, nil
}

func sourcedRule(o ruleHolderOrigin, rule string, generic bool) SourcedRule {
	return SourcedRule{Rule: rule, Kind: o.kind, HolderID: o.id, HolderName: o.name, Generic: generic}
}

// genericRuleLanding 是组通用层读引用组用的落点（command 表），shape / refShape 都在
// init 里 commandShape 注册之后填充。
var genericRuleLanding = &ruleLanding{
	refPolicyType: policyKindCommand,
}

// 策略列的 kind 词表（policy_group_entity 的 PolicyType* + k8s 列）。
const (
	policyKindCommand = policy.PolicyKindCommand
	policyKindQuery   = policy.PolicyKindQuery
	policyKindRedis   = policy.PolicyKindRedis
	policyKindMongo   = policy.PolicyKindMongo
	policyKindKafka   = policy.PolicyKindKafka
	policyKindK8s     = policy.PolicyKindK8s
	policyKindEtcd    = policy.PolicyKindEtcd
	policyKindOSS     = policy.PolicyKindOSS
)

// collectPolicyGroupRules 把引用的权限组里该形状的规则并入视图，并登记引用关系。
func collectPolicyGroupRules(ctx context.Context, landing *ruleLanding, refs []string, view *TypeRuleView, seen map[string]bool) {
	if len(refs) == 0 {
		return
	}
	shape := landing.refShape
	if shape == nil {
		return
	}
	for _, id := range refs {
		if seen[id] {
			continue
		}
		item, err := policy_group_svc.PolicyGroup().Get(ctx, id)
		if err != nil {
			// 引用悬空与运行时 Resolve* 的姿态一致：告警后跳过，不让一条坏引用
			// 挡住整个视图。
			logger.Ctx(ctx).Warn("resolve referenced policy group", zap.String("id", id), zap.Error(err))
			continue
		}
		seen[id] = true
		view.Groups = append(view.Groups, PolicyGroupRef{ID: item.ID, Name: item.Name})
		if item.PolicyType != landing.refPolicyType {
			continue
		}
		allow, deny, err := shape.groupSides(item.Policy)
		if err != nil {
			logger.Ctx(ctx).Warn("unmarshal policy group rules", zap.String("id", id), zap.Error(err))
			continue
		}
		for _, r := range allow {
			view.Allow = append(view.Allow, SourcedRule{Rule: r, Kind: RuleSourcePolicyGroup, PolicyGroupID: item.ID, PolicyGroupName: item.Name})
		}
		for _, r := range deny {
			view.Deny = append(view.Deny, SourcedRule{Rule: r, Kind: RuleSourcePolicyGroup, PolicyGroupID: item.ID, PolicyGroupName: item.Name})
		}
	}
}

// ShadowingDeny 返回视图中第一条会盖住 landedRule 的生效 deny（deny 无条件先判，
// permission.go:69-81 的同一优先序）；未被遮蔽返回 nil。
func ShadowingDeny(view *TypeRuleView, canonicalType, landedRule string) *SourcedRule {
	landing, ok := ruleLandingFor(canonicalType)
	if !ok || view == nil {
		return nil
	}
	for i, d := range view.Deny {
		match := landing.match
		if d.Generic {
			if landing.generic == nil {
				continue
			}
			match = landing.generic
		}
		if match(d.Rule, landedRule) {
			return &view.Deny[i]
		}
	}
	return nil
}

// --- 组目标 show：列出 holder 自身全部非空形状列 ---

// HolderShapeRules 是 holder 自身某一形状列的规则（policy show --group 用）。
type HolderShapeRules struct {
	PolicyType string
	Allow      []string
	Deny       []string
	Groups     []string
}

// ListHolderRuleShapes 按形状注册顺序列出 holder 自身非空的形状列。
// 注册表同时拥有读写落点和稳定顺序，新增形状无需再维护一份枚举镜像。
func ListHolderRuleShapes(holder policyent.Holder) ([]HolderShapeRules, error) {
	var shapes []HolderShapeRules
	for _, kind := range ruleShapeOrder {
		shape := ruleShapes[kind]
		allow, deny, refs, err := shape.ownSides(holder)
		if err != nil {
			return nil, fmt.Errorf("read %s policy shape: %w", kind, err)
		}
		if len(allow) == 0 && len(deny) == 0 && len(refs) == 0 {
			continue
		}
		shapes = append(shapes, HolderShapeRules{PolicyType: kind, Allow: allow, Deny: deny, Groups: refs})
	}
	return shapes, nil
}
