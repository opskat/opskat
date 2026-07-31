package helper

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"unicode"

	"github.com/opskat/opskat/internal/ai/cmdline"
)

// OSSCommand 是对象存储富命令串的结构形式。
//
// 格式：`<family> <verb> [target] [--flags]`，与 KafkaCommand 同形（family 占 Verb 位、
// verb 退到第一个 positional），因为 cmdline 只认"第一个词是动词"这一条词法规则，
// 而对象存储同样有 bucket / object 两层资源维度。
//
// Target 是 `<bucket>[/<key-or-prefix>]`；OSS 侧一条命令可能派生多条策略串
// （copy 两条、move 三条），所以对外是 PolicyStrings 而不是 kafka 的单串 PolicyString。
type OSSCommand struct {
	Family string
	Verb   string
	Target string
	Flags  map[string]string
}

// ossTargetKind 是一个 verb 的 target 形态（设计 §3.1 的语法）。
//
// 三态而不是"需不需要 target"的 bool：key 能不能省不是形式问题。派生串会原样成为
// grant 规则（§4.3），而规则侧 key 为空表示"该桶下任意深度的任意 key"（决策 D5），
// 于是一个省掉 key 的单对象命令会把一次单对象授权落成整桶授权——详见 validateOSSObjectKey。
type ossTargetKind int

const (
	// ossNoTarget：不取 target，`bucket list` 是唯一一个。
	ossNoTarget ossTargetKind = iota
	// ossPrefixTarget：`<bucket>[/<prefix>]`，只有 `object list`——列举本来就是前缀操作。
	ossPrefixTarget
	// ossObjectTarget：`<bucket>/<key>`，key 必须非空；其余全部 object verb 都作用在一个具体对象上。
	ossObjectTarget
)

// syntax 是报错里给模型看的 target 形态。
func (k ossTargetKind) syntax() string {
	if k == ossPrefixTarget {
		return "<bucket>[/<prefix>]"
	}
	return "<bucket>/<key>"
}

// ossVerbs 列出每个 family 支持的 verb 与它的 target 形态。
var ossVerbs = map[string]map[string]ossTargetKind{
	"bucket": {
		"list": ossNoTarget,
	},
	"object": {
		"list": ossPrefixTarget, "stat": ossObjectTarget, "get": ossObjectTarget,
		"put": ossObjectTarget, "copy": ossObjectTarget, "move": ossObjectTarget,
		"delete": ossObjectTarget, "presign": ossObjectTarget,
	},
}

// ParseOSSCommand 解析富命令串。
func ParseOSSCommand(s string) (*OSSCommand, error) {
	parsed, err := cmdline.Parse(s)
	if err != nil {
		return nil, err
	}

	verbs, ok := ossVerbs[parsed.Verb]
	if !ok {
		return nil, fmt.Errorf("unknown oss resource family %q; supported: %s",
			parsed.Verb, strings.Join(slices.Sorted(maps.Keys(ossVerbs)), ", "))
	}
	if len(parsed.Args) == 0 {
		return nil, fmt.Errorf("missing verb for %q; supported: %s",
			parsed.Verb, strings.Join(slices.Sorted(maps.Keys(verbs)), ", "))
	}

	verb := parsed.Args[0]
	kind, ok := verbs[verb]
	if !ok {
		return nil, fmt.Errorf("unknown verb %q for %q; supported: %s",
			verb, parsed.Verb, strings.Join(slices.Sorted(maps.Keys(verbs)), ", "))
	}
	if len(parsed.Args) > 2 {
		return nil, fmt.Errorf("oss commands take at most two positional arguments (<family> <verb> [target]); got %d", len(parsed.Args)+1)
	}

	c := &OSSCommand{Family: parsed.Verb, Verb: verb, Flags: parsed.Flags}
	if len(parsed.Args) == 2 {
		// target 的有无既是下界也是上界：`bucket list mybucket`（模型想说"列这个桶里的
		// 对象"）只当下界用的话会解析通过、审批弹窗照原样显示，执行器却把多出来的桶名
		// 丢掉、列出全部桶——批准一件事、拿到另一件。
		if kind == ossNoTarget {
			return nil, fmt.Errorf("verb %q takes no target, got %q; use: %s %s", verb, parsed.Args[1], c.Family, verb)
		}
		c.Target = parsed.Args[1]
	}
	if kind != ossNoTarget {
		if c.Target == "" {
			return nil, fmt.Errorf("verb %q requires a target: %s %s %s", verb, c.Family, verb, kind.syntax())
		}
		check := validateOSSResourceRef
		if kind == ossObjectTarget {
			check = validateOSSObjectRef
		}
		if err := check(c.Target); err != nil {
			return nil, fmt.Errorf("invalid target: %w", err)
		}
	}
	// 列举永远按前缀进行，bucket 根就是前缀 `<bucket>/`：归一化让"整桶授权"的规则形态
	// （`object.list mybucket/`）与列举命令自洽，否则同一个桶的根与其下任意前缀会派生出
	// 两种形状的策略串，一条规则盖不住。
	if kind == ossPrefixTarget && !strings.Contains(c.Target, "/") {
		c.Target += "/"
	}
	if err := validateOSSFlags(c); err != nil {
		return nil, err
	}
	return c, nil
}

// validateOSSResourceRef 校验一个 `<bucket>[/<key>]` 形态的资源引用——positional 的
// target 与 `--to` 的值都要过它，两者都会原样成为策略串的 resource 段。
//
// 前导/尾随空白必须拒绝，理由与 kafka 的 validateKafkaTarget 是同一条，只是切分规则
// 更宽：策略串在**第一段空白**处切成 (action, resource) 两段（决策 D4），带 padding 的
// 名字切出来的 resource 与任何规则都对不上，于是 deny 规则一条都匹配不上——静默的
// fail-open，而不是报错。判定谓词用 strings.TrimSpace（即 unicode.IsSpace），与匹配器
// 的切分谓词同源；这也顺带覆盖 shell 切词与 unicode.IsSpace 的错位：NBSP(U+00A0) /
// U+3000 不是 shell 分隔符，能不带引号一路走进 target。
//
// 与 kafka 的有意分歧是**内部**空白照收：`mybucket/My Report.pdf` 在对象存储里是再
// 常见不过的 key，而按第一段空白切分保证了它同样恒为两段。
func validateOSSResourceRef(ref string) error {
	switch {
	case ref == "":
		return fmt.Errorf("must be <bucket>[/<key>], got an empty value")
	case strings.HasPrefix(ref, "/"):
		return fmt.Errorf("%q cannot start with \"/\": it names a bucket and a key (<bucket>/<key>), not an absolute path", ref)
	case strings.HasPrefix(ref, "--"):
		// ParseOSSCommand 的 positional 永远到不了这里（这种词在切词阶段就被当 flag
		// 吃掉了），但 `--to=--x` 够得着，Render 的输出也必须重新解析回同一条命令。
		return fmt.Errorf("%q cannot start with \"--\": it would be misread as a flag when the rendered command is re-parsed", ref)
	case strings.TrimSpace(ref) != ref:
		return fmt.Errorf("%q cannot have leading or trailing whitespace: permission rules are matched after splitting \"<action> <resource>\" at its first whitespace, so a padded name cannot be authorized at all (a key may contain interior spaces)", ref)
	}
	return nil
}

// validateOSSObjectKey 要求引用**指名一个对象**：形如 `<bucket>/<key>` 且 key 非空。
// 单对象 verb 的 target 与 copy / move 的 `--to` 都要过它（§3.1 的语法；§3.3 写明
// resource 段为 `<bucket>/<key>`）。调用方先过 validateOSSResourceRef 做形状校验。
//
// 不是形式主义：派生串既是被匹配的命令，也会**原样成为规则**——§4.3 的 ossGrantPatterns
// 把 PolicyStrings() 直接存成 grant，而 policy.MatchOSSRule 里规则侧 key 为空（只写桶名，
// 或以 "/" 收尾）表示"该桶下任意深度的任意 key"（决策 D5，
// internal/ai/policy/oss_policy.go:88-90）。于是 `object stat mybucket/` 派生出
// "object.read mybucket/"：用户在审批弹窗上批准的是一个对象，选一次 "allow all"
// 落库的却是整桶可读的常驻 grant。空 key 在对象存储里也没有任何合法用途
// （StatObject 的 key 为空必然报错），属于"批准之后必然失败"的调用，要在弹窗之前失败。
func validateOSSObjectKey(ref string) error {
	bucket, key, ok := strings.Cut(ref, "/")
	if !ok || bucket == "" || key == "" {
		return fmt.Errorf("%q must name a single object as <bucket>/<key> with a non-empty key: a keyless resource authorizes every object in the bucket", ref)
	}
	return nil
}

// validateOSSObjectRef 是 `--to` 这类"第二个资源引用"的完整校验：形状 + 指名一个对象。
func validateOSSObjectRef(ref string) error {
	if err := validateOSSResourceRef(ref); err != nil {
		return err
	}
	return validateOSSObjectKey(ref)
}

// ossFlagCheck 校验单个 flag 值的形状；nil 表示自由文本。
type ossFlagCheck func(value string) error

type ossFlags = map[string]ossFlagCheck

// ossText 表示不约束形状。写成具名的 nil，比在表里散落 nil 好读。
var ossText ossFlagCheck

// ossFlagSpec 是某个 (family, verb) 认得的 flag 与其值的形状。
// required 收的是"缺了这条命令必然跑不成"的 flag：`object put` 没有 --file 就没有数据源，
// copy / move 没有 --to 就连策略串都少一条。
type ossFlagSpec struct {
	required ossFlags
	optional ossFlags
}

// ossPresignMethod 只收 get / put，且区分大小写。
//
// 严格是有理由的：这个值选的是 object.presign.read 还是 object.presign.write，而
// presign PUT 是默认策略里唯一被地板拒绝的操作。一个没认出来的值若落回 "get"，
// 就是把一次写授权静默降级成读授权。
func ossPresignMethod(value string) error {
	switch value {
	case "get", "put":
		return nil
	}
	return fmt.Errorf("must be get or put, got %q", value)
}

// ossFlagRules 列出每个 (family, verb) 认得的 flag 与其值的形状。
// 不在表里的 flag 一律拒绝：静默丢弃意味着审批弹窗上写着 `--recursive`、执行器却只删
// 一个对象——用户批准的和发生的不是一件事。
//
// 整数形状直接复用同包的 kafkaInt，不另抄一份：判定谓词与 aictx.ArgInt64 的 string
// 分支一致（`1,000` / `1e3` / `3.0` 会被解析成 0，然后被服务端默认值顶掉），
// 两份副本迟早分叉。
var ossFlagRules = map[string]map[string]ossFlagSpec{
	"bucket": {
		"list": {},
	},
	"object": {
		"list": {optional: ossFlags{"max-keys": kafkaInt, "after": ossText}},
		"stat": {},
		"get":  {optional: ossFlags{"file": ossText, "max-bytes": kafkaInt}},
		"put":  {required: ossFlags{"file": ossText}, optional: ossFlags{"content-type": ossText}},
		// --to 是第二个资源引用，与单对象 verb 的 target 同规：它会原样成为 object.write
		// 那条策略串的 resource 段，所以同样必须指名一个对象——`--to=dst` 派生出的
		// "object.write dst" 当规则用是整桶可写。
		"copy":    {required: ossFlags{"to": validateOSSObjectRef}},
		"move":    {required: ossFlags{"to": validateOSSObjectRef}},
		"delete":  {},
		"presign": {optional: ossFlags{"expiry": kafkaInt, "method": ossPresignMethod}},
	},
}

// validateOSSFlags 拒绝未知 flag、缺失的必填 flag 与坏形状的值。
//
// 三者都排在权限检查之前（解析期），理由与 kafka 的 validateKafkaFlags 一致：这些调用
// 批准之后必然失败，必须在弹审批对话框之前失败，否则用户先被打断批准一次
// （选 "allow all" 还会落一条常驻 grant），命令才失败。
func validateOSSFlags(c *OSSCommand) error {
	spec, ok := ossFlagRules[c.Family][c.Verb]
	if !ok {
		return fmt.Errorf("no flag rules registered for %q %q", c.Family, c.Verb)
	}
	known := make(ossFlags, len(spec.required)+len(spec.optional))
	maps.Copy(known, spec.required)
	maps.Copy(known, spec.optional)

	// unknown 排序后一次性报出：c.Flags 是 map，只报"碰到的第一个"会让同一条命令在不同次
	// 调用里报出不同的 flag 名，而这段文本是发给模型的。
	var unknown []string
	for name := range c.Flags {
		if _, ok := known[name]; !ok {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) > 0 {
		slices.Sort(unknown)
		return fmt.Errorf("unknown flag(s) --%s for %q %q; supported: %s",
			strings.Join(unknown, ", --"), c.Family, c.Verb, ossSupportedFlagList(spec))
	}

	// 必填先于形状：`--to` 完全没给和给了个坏值是两条不同的提示，先报缺失更准。
	var missing []string
	for name := range spec.required {
		if strings.TrimSpace(c.Flags[name]) == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		slices.Sort(missing)
		return fmt.Errorf("%q %q requires --%s", c.Family, c.Verb, strings.Join(missing, ", --"))
	}

	// 名字排序遍历，保证同一条命令每次报出同一个 flag。
	for _, name := range slices.Sorted(maps.Keys(c.Flags)) {
		check := known[name]
		if check == nil {
			continue
		}
		if err := check(c.Flags[name]); err != nil {
			return fmt.Errorf("invalid value for --%s: %w", name, err)
		}
	}
	return nil
}

func ossSupportedFlagList(spec ossFlagSpec) string {
	all := slices.Concat(slices.Collect(maps.Keys(spec.required)), slices.Collect(maps.Keys(spec.optional)))
	if len(all) == 0 {
		return "this verb takes no flags"
	}
	slices.Sort(all)
	return "--" + strings.Join(all, ", --")
}

// Render 是 ParseOSSCommand 的逆函数（TestOSSCommand_RoundTrip 锁住），也是审批弹窗与
// 审计行看到的展示态（决策 D6：规范形式是 DSL 串，匹配用派生出的策略串）。
//
// 拒绝以 "--" 开头的 Target，理由与 KafkaCommand.Render / MongoCommand.Render 完全一致：
// cmdline.Parse 判断 positional 还是 flag，看的是**剥掉引号之后**的词是否以 "--" 开头，
// 判断发生在引号信息已经丢失之后，所以加引号救不了。ParseOSSCommand 自己永远产不出
// 这种 Target，但 OSSCommand 是导出结构体，统一 exec 会用结构化的工具参数直接构造它、
// 绕开 Parse——拒绝好过悄悄产出一个 Render 完语义就变了的串。flag 名同理，判定复用
// cmdline.ValidFlagName。
func (c *OSSCommand) Render() (string, error) {
	if strings.HasPrefix(c.Target, "--") {
		return "", fmt.Errorf("target %q cannot start with \"--\": it would be misread as a flag when the rendered command is re-parsed", c.Target)
	}
	for _, name := range slices.Sorted(maps.Keys(c.Flags)) {
		if !cmdline.ValidFlagName(name) {
			return "", fmt.Errorf("invalid flag name %q: it would not survive re-parsing of the rendered command; use only letters, digits, and _@%%+:,./- characters", name)
		}
	}

	cmd := &cmdline.Command{Verb: c.Family, Args: []string{c.Verb}, Flags: c.Flags}
	if c.Target != "" {
		cmd.Args = append(cmd.Args, c.Target)
	}
	return cmd.Render(), nil
}

// PolicyStrings 返回策略匹配用的 "<action> <resource>" 串，每条恰好两段。
//
// copy / move 派生多条是本设计的核心安全点（决策 D7）：只检查目的地就等于放行
// "把受限对象复制到可读位置再读"的绕过路径。调用方要按"任一条被 deny 即整条命令被拒、
// allow 名单存在时必须每条都命中才放行"来消费这个切片。
//
// 后置条件（每条都能被"第一段空白"切成两段）不是可有可无的防御性代码，别顺手删：
// ParseOSSCommand 的 target 校验只守住"解析命令串"这一条路，而 OSSCommand 是导出结构体，
// 统一 exec / opsctl batch 会从结构化参数直接构造它、完全绕开解析。这条后置条件是那条缝上
// 唯一的守卫，而这条缝一旦破了是**静默**的——切不出两段时匹配器对任何规则都返回 false，
// deny 规则一条都不匹配（fail-open），allow 名单侧则永远不放行。宁可在这里 fail closed
// 报错，也不能把一个匹配不上任何规则的串交出去。
func (c *OSSCommand) PolicyStrings() ([]string, error) {
	actions, err := c.policyActions()
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(actions))
	for _, a := range actions {
		s := a.action + " " + a.resource
		if a.action == "" || strings.ContainsFunc(a.action, unicode.IsSpace) ||
			a.resource == "" || strings.TrimSpace(a.resource) != a.resource {
			return nil, fmt.Errorf("oss permission command %q does not split into exactly two segments \"<action> <resource>\": it would match no policy rule at all (neither allow nor deny)", s)
		}
		// 第二条后置条件，与两段性同源、方向相反：单对象操作的 resource 必须指名一个对象。
		// 切不出两段是 fail-open 到"匹配不上任何规则"，桶级的 resource 则是 fail-open 到
		// "匹配上整个桶"——派生串会原样落成 grant 规则（§4.3），规则侧空 key 表示该桶下
		// 任意深度（决策 D5）。同样是 ParseOSSCommand 够不到的那条缝（结构化参数直接
		// 构造 OSSCommand）上的唯一守卫。
		if !a.prefix {
			if err := validateOSSObjectKey(a.resource); err != nil {
				return nil, fmt.Errorf("oss permission command %q does not name a single object: %w", s, err)
			}
		}
		out = append(out, s)
	}
	return out, nil
}

// ossPolicyAction 是一条策略串的两段形式，拼接与后置条件检查都在 PolicyStrings 里做。
// prefix 标记 resource 段本来就是前缀而非单个对象（`bucket.list *`、`object.list`），
// 它是整桶/整前缀语义的**唯一**合法来源。
type ossPolicyAction struct {
	action   string
	resource string
	prefix   bool
}

// policyActions 按设计 §3.3 的表格派生策略串。
func (c *OSSCommand) policyActions() ([]ossPolicyAction, error) {
	switch c.Family {
	case "bucket":
		if c.Verb == "list" {
			// 解析期同款上界，守的是结构化参数直接构造 OSSCommand 那条缝：Target 被静默
			// 丢掉的话，审批弹窗按 Render 显示 `bucket list mybucket`（一个桶），
			// 检查与落库的却是 `bucket.list *`（全部桶）——批准一件事、拿到另一件。
			if c.Target != "" {
				return nil, fmt.Errorf("%q %q takes no target, got %q: it derives \"bucket.list *\" (every bucket), which is not what the rendered command shows", c.Family, c.Verb, c.Target)
			}
			// 桶列举没有具体资源，用 "*" 占住 resource 段——策略串恒为两段。
			return []ossPolicyAction{{action: "bucket.list", resource: "*", prefix: true}}, nil
		}
	case "object":
		switch c.Verb {
		case "list":
			return []ossPolicyAction{{action: "object.list", resource: c.Target, prefix: true}}, nil
		case "stat", "get":
			return []ossPolicyAction{{action: "object.read", resource: c.Target}}, nil
		case "put":
			return []ossPolicyAction{{action: "object.write", resource: c.Target}}, nil
		case "delete":
			return []ossPolicyAction{{action: "object.delete", resource: c.Target}}, nil
		case "copy":
			return []ossPolicyAction{
				{action: "object.read", resource: c.Target},
				{action: "object.write", resource: c.Flags["to"]},
			}, nil
		case "move":
			return []ossPolicyAction{
				{action: "object.read", resource: c.Target},
				{action: "object.write", resource: c.Flags["to"]},
				{action: "object.delete", resource: c.Target},
			}, nil
		case "presign":
			// 缺省是 get：预签名读是常见用法，而 put 必须由模型显式写出来——它是默认
			// 策略里唯一被地板拒绝的操作。认不出来的值 fail closed，不回落成 read：
			// 那等于把一次写授权静默降级成读授权。解析期已经拦过一遍，这里守的是
			// 直接构造 OSSCommand 的那条缝，合法值只有 ossPresignMethod 一处真相。
			method := c.Flags["method"]
			if method == "" {
				method = "get"
			}
			if err := ossPresignMethod(method); err != nil {
				return nil, fmt.Errorf("invalid value for --method: %w", err)
			}
			if method == "put" {
				return []ossPolicyAction{{action: "object.presign.write", resource: c.Target}}, nil
			}
			return []ossPolicyAction{{action: "object.presign.read", resource: c.Target}}, nil
		}
	}
	return nil, fmt.Errorf("unknown oss command %q %q", c.Family, c.Verb)
}
