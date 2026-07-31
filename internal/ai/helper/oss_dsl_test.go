package helper

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"unicode"

	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/ai/policy"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

// withOSSPolicyStringsWiring 在测试期做 internal/ai/execimpl 在 init() 里做的那件事：
// 把本包的 DSL 派生推给 permission。本包不能被 execimpl 反过来 import（cycle），
// 所以要端到端跑完"DSL → 策略串 → grant 规则"这条链，只能在这里自己接一次线。
// 接的是**真派生**而不是一张桩表，否则这条链上最容易漂移的那一段就没人比对了。
func withOSSPolicyStringsWiring(t *testing.T) {
	t.Helper()
	permission.RegisterPolicyStrings(asset_entity.AssetTypeOSS, func(command string) ([]string, error) {
		parsed, err := ParseOSSCommand(command)
		if err != nil {
			return nil, err
		}
		return parsed.PolicyStrings()
	})
	t.Cleanup(func() { permission.UnregisterPolicyStringsForTest(asset_entity.AssetTypeOSS) })
}

func TestParseOSSCommand(t *testing.T) {
	cases := []struct {
		in   string
		want OSSCommand
	}{
		{`bucket list`, OSSCommand{Family: "bucket", Verb: "list", Flags: map[string]string{}}},
		{`object list logs/2026/ --max-keys=100 --after=a.txt`,
			OSSCommand{Family: "object", Verb: "list", Target: "logs/2026/",
				Flags: map[string]string{"max-keys": "100", "after": "a.txt"}}},
		{`object stat mybucket/key.txt`,
			OSSCommand{Family: "object", Verb: "stat", Target: "mybucket/key.txt", Flags: map[string]string{}}},
		{`object get mybucket/key.txt --file=/tmp/key.txt --max-bytes=1024`,
			OSSCommand{Family: "object", Verb: "get", Target: "mybucket/key.txt",
				Flags: map[string]string{"file": "/tmp/key.txt", "max-bytes": "1024"}}},
		{`object put mybucket/key.txt --file=/tmp/key.txt --content-type=text/plain`,
			OSSCommand{Family: "object", Verb: "put", Target: "mybucket/key.txt",
				Flags: map[string]string{"file": "/tmp/key.txt", "content-type": "text/plain"}}},
		{`object copy src/a.txt --to=dst/b.txt`,
			OSSCommand{Family: "object", Verb: "copy", Target: "src/a.txt",
				Flags: map[string]string{"to": "dst/b.txt"}}},
		{`object move src/a.txt --to=dst/b.txt`,
			OSSCommand{Family: "object", Verb: "move", Target: "src/a.txt",
				Flags: map[string]string{"to": "dst/b.txt"}}},
		{`object delete mybucket/key.txt`,
			OSSCommand{Family: "object", Verb: "delete", Target: "mybucket/key.txt", Flags: map[string]string{}}},
		{`object presign mybucket/key.txt --expiry=600 --method=put`,
			OSSCommand{Family: "object", Verb: "presign", Target: "mybucket/key.txt",
				Flags: map[string]string{"expiry": "600", "method": "put"}}},
	}
	for _, c := range cases {
		got, err := ParseOSSCommand(c.in)
		if err != nil {
			t.Fatalf("ParseOSSCommand(%q) unexpected error: %v", c.in, err)
		}
		if !reflect.DeepEqual(*got, c.want) {
			t.Fatalf("ParseOSSCommand(%q) = %#v, want %#v", c.in, *got, c.want)
		}
	}
}

// TestParseOSSCommand_NormalizesBareBucketListing 锁住 §3.2 的规范化：列举永远按前缀
// 进行，bucket 根就是前缀 `<bucket>/`。不规范化的话，`object list mybucket` 会派生出
// 策略串 "object.list mybucket"，而同一个桶下的任何前缀列举派生的都是 "mybucket/..."
// ——同一件事两种形状，用户写的授权规则只能盖住其中一种。
//
// 归一化只发生在 list 上，也只可能发生在 list 上：其余 object verb 的 target 必须自带
// 非空 key（见 TestParseOSSCommand_RejectsTargetWithoutObjectKey），补一个 "/" 反而会把
// 单对象操作变成整桶授权。
func TestParseOSSCommand_NormalizesBareBucketListing(t *testing.T) {
	cases := []struct{ in, want string }{
		{`object list mybucket`, "mybucket/"},
		{`object list mybucket/`, "mybucket/"},
		{`object list mybucket/logs`, "mybucket/logs"},
		{`object stat mybucket/key`, "mybucket/key"}, // 已经含 "/" 的 target 原样保留
	}
	for _, c := range cases {
		got, err := ParseOSSCommand(c.in)
		if err != nil {
			t.Fatalf("ParseOSSCommand(%q) unexpected error: %v", c.in, err)
		}
		if got.Target != c.want {
			t.Fatalf("ParseOSSCommand(%q).Target = %q, want %q", c.in, got.Target, c.want)
		}
	}
}

// TestParseOSSCommand_AcceptsInteriorWhitespaceInTarget 是与 kafka 的有意分歧（决策 D4）：
// 策略串按**第一段空白**切成两段，所以 key 里的空格不会让分段数出错，而
// `My Report.pdf` 这类 key 在对象存储里再常见不过——照搬 kafka 的"拒绝一切空白"
// 会让它们完全不可达。
func TestParseOSSCommand_AcceptsInteriorWhitespaceInTarget(t *testing.T) {
	cases := []struct{ in, want string }{
		{`object stat "mybucket/My Report.pdf"`, "mybucket/My Report.pdf"},
		{`object list "my bucket/2026 reports/"`, "my bucket/2026 reports/"},
		{"object stat 'mybucket/a\tb'", "mybucket/a\tb"},
	}
	for _, c := range cases {
		got, err := ParseOSSCommand(c.in)
		if err != nil {
			t.Fatalf("ParseOSSCommand(%q) unexpected error: %v", c.in, err)
		}
		if got.Target != c.want {
			t.Fatalf("ParseOSSCommand(%q).Target = %q, want %q", c.in, got.Target, c.want)
		}
	}
}

func TestParseOSSCommand_RejectsUnknownFamilyOrVerb(t *testing.T) {
	cases := []string{
		`nonsense list`,
		`object nonsense mybucket/key`,
		`bucket delete mybucket`,
		`object`,
		`bucket`,
	}
	for _, in := range cases {
		if _, err := ParseOSSCommand(in); err == nil {
			t.Fatalf("ParseOSSCommand(%q) = nil error, want rejection", in)
		}
	}
}

// TestParseOSSCommand_RejectsMissingTarget 锁住 needsTarget 的下界：所有 object verb
// 都必须取 target。
func TestParseOSSCommand_RejectsMissingTarget(t *testing.T) {
	cases := []string{
		`object list`,
		`object stat`,
		`object get --file=/tmp/a`,
		`object put --file=/tmp/a`,
		`object copy --to=dst/b`,
		`object move --to=dst/b`,
		`object delete`,
		`object presign --method=get`,
	}
	for _, in := range cases {
		if _, err := ParseOSSCommand(in); err == nil {
			t.Fatalf("ParseOSSCommand(%q) = nil error, want rejection (verb requires a target)", in)
		}
	}
}

// TestParseOSSCommand_RejectsExtraTarget 锁住上界：`bucket list mybucket` 不静默丢弃
// 多余的 target（审批弹窗会照原样显示它，执行器却列出全部桶——批准一件事、拿到另一件），
// `<family> <verb> [target]` 之外的 positional 同理。
func TestParseOSSCommand_RejectsExtraTarget(t *testing.T) {
	cases := []string{
		`bucket list mybucket`,
		`object stat mybucket/key extra`,
		`object list mybucket a b`,
	}
	for _, in := range cases {
		if _, err := ParseOSSCommand(in); err == nil {
			t.Fatalf("ParseOSSCommand(%q) = nil error, want rejection (too many positional arguments)", in)
		}
	}
}

// TestParseOSSCommand_RejectsTargetWithoutObjectKey 锁住 §3.1 的语法：除 `object list`
// （`<bucket>[/<prefix>]`）外，每个 object verb 的 target 与 copy/move 的 `--to` 都是
// `<bucket>/<key>`，key 不可为空。
//
// 这不是形式主义。派生出的 resource 段既是被匹配的命令，也会**原样成为规则**：§4.3 的
// ossGrantPatterns 把 PolicyStrings() 直接存成 grant。而 policy.MatchOSSRule 里规则侧
// key 为空（只写桶名，或以 "/" 收尾）表示"该桶/前缀下任意深度的任意 key"（决策 D5，
// internal/ai/policy/oss_policy.go:88-90）。于是 `object stat mybucket/` 派生出
// "object.read mybucket/"，用户在审批弹窗上批准的是**一个**对象、选一次 "allow all"
// 落库的却是**整桶可读**的常驻 grant。同理 `object copy src/a.txt --to=dst` 会落一条
// 整桶可写。空 key 在对象存储里也没有任何合法用途（StatObject("") 必然失败），
// 所以这类命令还是"批准之后必然失败"的那一类——必须在弹窗之前失败。
func TestParseOSSCommand_RejectsTargetWithoutObjectKey(t *testing.T) {
	cases := []string{
		`object stat mybucket`,
		`object stat mybucket/`,
		`object get mybucket`,
		`object get mybucket/`,
		`object put mybucket --file=/tmp/a`,
		`object delete mybucket`,
		`object delete mybucket/`,
		`object presign mybucket`,
		`object copy mybucket --to=dst/b.txt`,
		`object copy src/a.txt --to=dst`,
		`object copy src/a.txt --to=dst/`,
		`object move src/a.txt --to=dst`,
	}
	for _, in := range cases {
		if got, err := ParseOSSCommand(in); err == nil {
			t.Fatalf("ParseOSSCommand(%q) = %#v, nil error; want rejection (target must be <bucket>/<key>: a keyless resource authorizes the whole bucket)", in, *got)
		}
	}
	// 反向对照：list 的 target 本来就是前缀，桶根是合法写法（§3.2 会把它归一成 `mybucket/`）。
	if _, err := ParseOSSCommand(`object list mybucket`); err != nil {
		t.Fatalf("ParseOSSCommand(`object list mybucket`) = %v, want success (list takes <bucket>[/<prefix>])", err)
	}
}

// TestParseOSSCommand_RejectsInvalidTarget 覆盖 §3.2 的形状规则。前导/尾随空白是
// fail-open 本体：策略串在第一段空白处切开，一个带 padding 的 target 切出来的 resource
// 与任何规则都对不上——deny 规则跟着一条都匹配不上。NBSP 单列且不加引号：shell 切词
// 不认它（能一路走进 Target），unicode.IsSpace 认得。
func TestParseOSSCommand_RejectsInvalidTarget(t *testing.T) {
	cases := []string{
		`object stat ''`,
		`object stat /mybucket/key`,
		`object stat /`,
		`object stat ' mybucket/key'`,
		`object stat 'mybucket/key '`,
		"object stat mybucket/key\u00a0",
		"object stat \u00a0mybucket/key",
		"object list 'mybucket/logs\n'",
	}
	for _, in := range cases {
		if _, err := ParseOSSCommand(in); err == nil {
			t.Fatalf("ParseOSSCommand(%q) = nil error, want rejection (invalid target shape)", in)
		}
	}
}

// TestParseOSSCommand_RejectsUnknownFlag：多余 flag 静默丢弃意味着审批弹窗上写着
// `--recursive`、执行器却只删一个对象。
func TestParseOSSCommand_RejectsUnknownFlag(t *testing.T) {
	cases := []string{
		`bucket list --max-keys=10`,
		`object stat mybucket/key --file=/tmp/a`,
		`object delete mybucket/key --recursive`,
		`object list mybucket --max_keys=10`,
		`object get mybucket/key --expiry=600`,
		`object presign mybucket/key --file=/tmp/a`,
	}
	for _, in := range cases {
		if _, err := ParseOSSCommand(in); err == nil {
			t.Fatalf("ParseOSSCommand(%q) = nil error, want rejection (unknown flag)", in)
		}
	}
}

// TestParseOSSCommand_RejectsMissingRequiredFlag：缺了这些 flag 这条命令必然跑不成，
// 而 copy / move 缺 --to 还会让策略串少一条——弹窗上问的是一件事，检查的是另一件。
func TestParseOSSCommand_RejectsMissingRequiredFlag(t *testing.T) {
	cases := []string{
		`object put mybucket/key`,
		`object put mybucket/key --content-type=text/plain`,
		`object copy src/a.txt`,
		`object move src/a.txt`,
	}
	for _, in := range cases {
		if _, err := ParseOSSCommand(in); err == nil {
			t.Fatalf("ParseOSSCommand(%q) = nil error, want rejection (required flag missing)", in)
		}
	}
}

// TestParseOSSCommand_RejectsBadFlagValue 把"批准之后必然失败"的调用挡在弹窗之前：
// 形状校验发生在解析期，也就是权限检查之前。--method 尤其要紧——它选的是
// object.presign.read 还是 object.presign.write，一个没认出来的值若落回 read
// 就是静默的 fail-open。
func TestParseOSSCommand_RejectsBadFlagValue(t *testing.T) {
	cases := []string{
		`object list mybucket --max-keys=1,000`,
		`object list mybucket --max-keys=1e3`,
		`object get mybucket/key --max-bytes=3.0`,
		`object presign mybucket/key --expiry=10_000`,
		`object presign mybucket/key --method=post`,
		`object presign mybucket/key --method=GET`,
		`object presign mybucket/key --method`,
		`object copy src/a --to=/dst/b`,
		`object copy src/a --to=' dst/b'`,
		`object move src/a --to=`,
		fmt.Sprintf(`object list mybucket --max-keys=%d`, ossMaxListKeys+1),
		fmt.Sprintf(`object presign mybucket/key --expiry=%d`, ossMaxPresignExpirySecs+1),
	}
	for _, in := range cases {
		if _, err := ParseOSSCommand(in); err == nil {
			t.Fatalf("ParseOSSCommand(%q) = nil error, want rejection (invalid flag value)", in)
		}
	}
}

// TestParseOSSCommand_MaxKeysHasAHardCap 锁住 --max-keys 的上界本身（而不只是"某个大数
// 被拒"）：模型能一次把一个前缀的全部 key 拉进上下文，与 --max-bytes 不设上限时能拉整个
// 大对象是同一个顾虑（决策见任务交回记录）。边界必须精确到"刚好等于上限"与"多 1"两个
// 用例——只断言一个很大的数会被拒，改动上限值本身不会让测试失败。
func TestParseOSSCommand_MaxKeysHasAHardCap(t *testing.T) {
	if _, err := ParseOSSCommand(fmt.Sprintf("object list mybucket --max-keys=%d", ossMaxListKeys)); err != nil {
		t.Fatalf("ParseOSSCommand with --max-keys=%d (the cap itself) unexpected error: %v", ossMaxListKeys, err)
	}
	if _, err := ParseOSSCommand(fmt.Sprintf("object list mybucket --max-keys=%d", ossMaxListKeys+1)); err == nil {
		t.Fatalf("ParseOSSCommand with --max-keys=%d = nil error, want rejection (one over the cap)", ossMaxListKeys+1)
	}
}

// TestParseOSSCommand_ExpiryHasAHardCap 锁住 --expiry 的上界：presign 的有效期没有上限时，
// 模型批准的命令会在执行时才被存储后端以"超过 7 天"为由拒掉（S3 兼容后端对 SigV4
// 预签名 URL 的通行约束）——这正是 §3.1 要挡在弹窗之前的那一类"批准之后必然失败"。
func TestParseOSSCommand_ExpiryHasAHardCap(t *testing.T) {
	if _, err := ParseOSSCommand(fmt.Sprintf("object presign mybucket/key --expiry=%d", ossMaxPresignExpirySecs)); err != nil {
		t.Fatalf("ParseOSSCommand with --expiry=%d (the cap itself) unexpected error: %v", ossMaxPresignExpirySecs, err)
	}
	if _, err := ParseOSSCommand(fmt.Sprintf("object presign mybucket/key --expiry=%d", ossMaxPresignExpirySecs+1)); err == nil {
		t.Fatalf("ParseOSSCommand with --expiry=%d = nil error, want rejection (one second over the cap)", ossMaxPresignExpirySecs+1)
	}
}

// TestOSSCommand_RoundTrip 是 Parse→Render→Parse 的往返性质：Render 出来的串重新解析
// 必须得到同一个结构，且再渲染一次逐字节相同（规范化是幂等的）。Render 的输出是审批
// 弹窗与审计行看到的那一串——它若不能被自己解析回来，用户批准的和执行的就是两条命令。
func TestOSSCommand_RoundTrip(t *testing.T) {
	cases := []string{
		`bucket list`,
		`object list mybucket`,
		`object list mybucket/logs/ --max-keys=100 --after=a.txt`,
		`object stat "mybucket/My Report.pdf"`,
		`object get mybucket/key.txt --file=/tmp/key.txt --max-bytes=1024`,
		`object put mybucket/key.txt --file=/tmp/key.txt --content-type=application/json`,
		`object copy "src/My Report.pdf" --to="dst/My Report.pdf"`,
		`object move src/a.txt --to=dst/b.txt`,
		`object delete mybucket/key.txt`,
		`object presign mybucket/key.txt --expiry=600 --method=put`,
	}
	for _, in := range cases {
		first, err := ParseOSSCommand(in)
		if err != nil {
			t.Fatalf("ParseOSSCommand(%q) unexpected error: %v", in, err)
		}
		rendered, err := first.Render()
		if err != nil {
			t.Fatalf("Render() for %q unexpected error: %v", in, err)
		}
		second, err := ParseOSSCommand(rendered)
		if err != nil {
			t.Fatalf("ParseOSSCommand(Render(%q)) = error %v (rendered: %s)", in, err, rendered)
		}
		if !reflect.DeepEqual(*first, *second) {
			t.Fatalf("round-trip of %q = %#v, want %#v (rendered: %s)", in, *second, *first, rendered)
		}
		again, err := second.Render()
		if err != nil {
			t.Fatalf("Render() of re-parsed %q unexpected error: %v", in, err)
		}
		if again != rendered {
			t.Fatalf("Render() is not idempotent for %q: %q then %q", in, rendered, again)
		}
	}
}

// TestOSSCommand_RoundTripNormalizesNilFlags：手工构造（统一 exec 的结构化参数路径）
// 允许 Flags 为 nil，ParseOSSCommand 永远返回非 nil map，所以往返回来的是空 map。
func TestOSSCommand_RoundTripNormalizesNilFlags(t *testing.T) {
	src := OSSCommand{Family: "object", Verb: "stat", Target: "mybucket/key.txt"}
	rendered, err := src.Render()
	if err != nil {
		t.Fatalf("Render(%#v) unexpected error: %v", src, err)
	}
	got, err := ParseOSSCommand(rendered)
	if err != nil {
		t.Fatalf("ParseOSSCommand(%q) unexpected error: %v", rendered, err)
	}
	want := OSSCommand{Family: "object", Verb: "stat", Target: "mybucket/key.txt", Flags: map[string]string{}}
	if !reflect.DeepEqual(*got, want) {
		t.Fatalf("round-trip of nil-Flags command = %#v, want %#v", *got, want)
	}
}

// TestOSSCommand_RenderRejectsFlagLikeTarget：cmdline.Parse 判断 positional 还是 flag
// 看的是**剥掉引号之后**的词是否以 "--" 开头，所以加引号救不了。ParseOSSCommand 自己
// 产不出这种 Target（这种词在切词阶段就被当 flag 吃掉了），但 OSSCommand 是导出结构体。
func TestOSSCommand_RenderRejectsFlagLikeTarget(t *testing.T) {
	cases := []OSSCommand{
		{Family: "object", Verb: "stat", Target: "--file=/etc/passwd"},
		{Family: "object", Verb: "stat", Target: "--"},
	}
	for _, c := range cases {
		if got, err := c.Render(); err == nil {
			t.Fatalf("Render(%#v) = %q, nil error; want rejection (target looks like a flag)", c, got)
		}
	}
}

// TestOSSCommand_RenderRejectsUnsafeFlagName：cmdline.Command.Render 明说手写字面量
// 不受 Parse 的入口校验保护、由调用方保证 flag 名合法——本函数就是那个调用方。
// `{"cfg=x": "1"}` 渲染成 --cfg=x=1，重新解析回来是 flag "cfg" 值 "x=1"。
func TestOSSCommand_RenderRejectsUnsafeFlagName(t *testing.T) {
	cases := []OSSCommand{
		{Family: "object", Verb: "get", Target: "mybucket/key", Flags: map[string]string{"cfg=x": "1"}},
		{Family: "object", Verb: "get", Target: "mybucket/key", Flags: map[string]string{"a b": "1"}},
		{Family: "object", Verb: "get", Target: "mybucket/key", Flags: map[string]string{"": "1"}},
	}
	for _, c := range cases {
		if got, err := c.Render(); err == nil {
			t.Fatalf("Render(%#v) = %q, nil error; want rejection (flag name does not survive re-parsing)", c, got)
		}
	}
}

// TestOSSCommand_PolicyStrings 是本任务最要紧的测试：右侧的 want 直接抄自设计 §3.3 的
// 表格，不是"跑一遍看看输出什么"填进来的。copy 派生两条、move 派生三条是核心安全点——
// 只检查目的地等于放行"把受限对象复制到可读位置再读"这条绕过路径。
func TestOSSCommand_PolicyStrings(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{`bucket list`, []string{"bucket.list *"}},
		{`object list mybucket`, []string{"object.list mybucket/"}},
		{`object list mybucket/logs/`, []string{"object.list mybucket/logs/"}},
		{`object stat mybucket/key.txt`, []string{"object.read mybucket/key.txt"}},
		{`object get mybucket/key.txt`, []string{"object.read mybucket/key.txt"}},
		{`object put mybucket/key.txt --file=/tmp/a`, []string{"object.write mybucket/key.txt"}},
		{`object copy src/a.txt --to=dst/b.txt`,
			[]string{"object.read src/a.txt", "object.write dst/b.txt"}},
		{`object move src/a.txt --to=dst/b.txt`,
			[]string{"object.read src/a.txt", "object.write dst/b.txt", "object.delete src/a.txt"}},
		{`object delete mybucket/key.txt`, []string{"object.delete mybucket/key.txt"}},
		{`object presign mybucket/key.txt`, []string{"object.presign.read mybucket/key.txt"}},
		{`object presign mybucket/key.txt --method=get`, []string{"object.presign.read mybucket/key.txt"}},
		{`object presign mybucket/key.txt --method=put`, []string{"object.presign.write mybucket/key.txt"}},
		{`object stat "mybucket/My Report.pdf"`, []string{"object.read mybucket/My Report.pdf"}},
	}
	for _, c := range cases {
		parsed, err := ParseOSSCommand(c.in)
		if err != nil {
			t.Fatalf("ParseOSSCommand(%q) unexpected error: %v", c.in, err)
		}
		got, err := parsed.PolicyStrings()
		if err != nil {
			t.Fatalf("PolicyStrings() for %q unexpected error: %v", c.in, err)
		}
		if !slices.Equal(got, c.want) {
			t.Fatalf("PolicyStrings() for %q = %q, want %q — a mismatch here silently fails the deny list open",
				c.in, got, c.want)
		}
		for _, s := range got {
			assertTwoSegmentOSSPolicyString(t, c.in, s)
		}
	}
}

// TestOSSCommand_PolicyStringsKeepTheKeyVerbatim 锁住决策 D21 的**更正**：派生串在这里
// 是被拿去撞规则的**名字**，key 段一个字节都不许动。
//
// 转义属于另一个角色。同一条串落成常驻 grant 时才转义，那一步在
// permission.NormalizeGrantPatterns —— 两边都转义的结果是
// path.Match(`b/secrets\*`, `b/secrets\*`) = false：含通配元字符的 key 上"始终允许"
// 从此不生效，每次重新弹框，还多落一条什么都不授权的死行（c0de1b2c 的实况）。
// 收窄本身由 TestOSSCommand_GrantsDerivedFromAKeyDoNotAuthorizeAnother 端到端咬住。
func TestOSSCommand_PolicyStringsKeepTheKeyVerbatim(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{`object get "mybucket/secrets*"`, []string{`object.read mybucket/secrets*`}},
		{`object stat "mybucket/logs/a[1].log"`, []string{`object.read mybucket/logs/a[1].log`}},
		{`object delete "mybucket/what?.txt"`, []string{`object.delete mybucket/what?.txt`}},
		{`object put 'mybucket/a\b.txt' --file=/tmp/a`, []string{`object.write mybucket/a\b.txt`}},
		{`object copy "src/a*.txt" --to="dst/b[1].txt"`,
			[]string{`object.read src/a*.txt`, `object.write dst/b[1].txt`}},
		{`object presign "mybucket/re[port].pdf" --method=put`, []string{`object.presign.write mybucket/re[port].pdf`}},
		{`object list "mybucket/logs[1]"`, []string{`object.list mybucket/logs[1]`}},
		{`object list "mybucket/logs[1]/"`, []string{`object.list mybucket/logs[1]/`}},
		{`object list 'mybucket/a\b/'`, []string{`object.list mybucket/a\b/`}},
		{`object get mybucket/logs/app.log`, []string{"object.read mybucket/logs/app.log"}},
		{`bucket list`, []string{"bucket.list *"}},
	}
	for _, c := range cases {
		parsed, err := ParseOSSCommand(c.in)
		if err != nil {
			t.Fatalf("ParseOSSCommand(%q) unexpected error: %v", c.in, err)
		}
		got, err := parsed.PolicyStrings()
		if err != nil {
			t.Fatalf("PolicyStrings() for %q unexpected error: %v", c.in, err)
		}
		if !slices.Equal(got, c.want) {
			t.Errorf("PolicyStrings() for %q = %q, want %q", c.in, got, c.want)
		}
		for _, s := range got {
			assertTwoSegmentOSSPolicyString(t, c.in, s)
		}
	}
}

// TestOSSCommand_GrantsDerivedFromAKeyDoNotAuthorizeAnother 是 D21 的安全后置条件本体，
// 端到端跑完生产那条链：DSL → PolicyStrings()（名字）→ permission.NormalizeGrantPatterns
// （规则）→ policy.MatchOSSRule。三段各在一个包里，接错任何一段这条断言都会红。
//
// 两个方向都要成立，缺一个就能被廉价地满足：
//   - 落下的 grant 不能覆盖**别的** key——否则批准读一个对象换来读一批；
//   - 落下的 grant 必须覆盖**这一个** key 自己——否则它是一条死行，"始终允许"永远不生效
//     （c0de1b2c 的两边都转义正是这样，而它同样能通过上一条）。
func TestOSSCommand_GrantsDerivedFromAKeyDoNotAuthorizeAnother(t *testing.T) {
	withOSSPolicyStringsWiring(t)

	cases := []struct {
		in    string
		other []string
	}{
		{`object get "mybucket/secrets*"`, []string{
			"object.read mybucket/secretsFOO",
			"object.read mybucket/secrets.env",
		}},
		{`object delete "mybucket/logs/a[1].log"`, []string{
			"object.delete mybucket/logs/a1.log",
		}},
		{`object list "mybucket/logs[1]"`, []string{
			"object.list mybucket/logs1",
		}},
		{`object copy "src/a?.txt" --to="dst/b*.txt"`, []string{
			"object.read src/ax.txt",
			"object.write dst/bOTHER.txt",
		}},
		// 桶段同样要收窄：`object get 'my*/k'` 派生的规则不转义就是跨桶授权。
		{`object get "my*/k"`, []string{"object.read mybucket/k"}},
	}
	for _, c := range cases {
		parsed, err := ParseOSSCommand(c.in)
		if err != nil {
			t.Fatalf("ParseOSSCommand(%q) unexpected error: %v", c.in, err)
		}
		names, err := parsed.PolicyStrings()
		if err != nil {
			t.Fatalf("PolicyStrings() for %q unexpected error: %v", c.in, err)
		}
		canonical, err := parsed.Render()
		if err != nil {
			t.Fatalf("Render() for %q unexpected error: %v", c.in, err)
		}
		rules := permission.NormalizeGrantPatterns(
			asset_entity.AssetTypeOSS, canonical, permission.GrantOriginSystem)
		if len(rules) != len(names) {
			t.Fatalf("%q derives %d policy strings but lands as %d grants (%q); "+
				"the assertions below would not line up", c.in, len(names), len(rules), rules)
		}
		for i, rule := range rules {
			if !policy.MatchOSSRule(rule, names[i]) {
				t.Errorf("%q lands as grant %q, which does not authorize the very request %q "+
					`it came from — a dead row, and "always allow" never takes effect`, c.in, rule, names[i])
			}
			for _, other := range c.other {
				if policy.MatchOSSRule(rule, other) {
					t.Errorf("%q lands as grant %q, which also authorizes %q", c.in, rule, other)
				}
			}
		}
	}

	// 反过来，收窄不能把派生串挪出用户自己写的通配规则之外：`dist/*` 是最常见的一条
	// 授权，而 `dist/a[1].js` 是递归展开合法产出的 key。这里比的是**名字**——名字侧
	// 一旦被转义，这条断言就会红。
	parsed, err := ParseOSSCommand(`object get "mybucket/dist/a[1].js"`)
	if err != nil {
		t.Fatalf("ParseOSSCommand: %v", err)
	}
	got, err := parsed.PolicyStrings()
	if err != nil {
		t.Fatalf("PolicyStrings(): %v", err)
	}
	if !policy.MatchOSSRule("object.read mybucket/dist/*", got[0]) {
		t.Errorf("%q is no longer covered by the user-written rule %q", got[0], "object.read mybucket/dist/*")
	}
}

// TestOSSCommand_PolicyStringsCoversEveryVerb 把 ossVerbs 与策略串派生这两份独立维护的
// 清单钉在一起：表里多写一个派生不出策略串的 verb，症状是命令解析通过、策略串却拿不到,
// 统一 exec 下该操作直接不可达。9 是设计 §3.1 列出的命令条数。
func TestOSSCommand_PolicyStringsCoversEveryVerb(t *testing.T) {
	total := 0
	for family, verbs := range ossVerbs {
		for verb, kind := range verbs {
			total++
			c := &OSSCommand{Family: family, Verb: verb, Flags: map[string]string{}}
			if kind != ossNoTarget {
				c.Target = "mybucket/key"
			}
			if verb == "copy" || verb == "move" {
				c.Flags["to"] = "dst/key"
			}
			got, err := c.PolicyStrings()
			if err != nil {
				t.Fatalf("PolicyStrings() for %q %q unexpected error: %v", family, verb, err)
			}
			if len(got) == 0 {
				t.Fatalf("PolicyStrings() for %q %q = empty; every command must be matched against at least one rule", family, verb)
			}
			for _, s := range got {
				assertTwoSegmentOSSPolicyString(t, family+" "+verb, s)
			}
		}
	}
	if total != 9 {
		t.Fatalf("ossVerbs has %d commands, want 9 (design §3.1 lists exactly nine)", total)
	}
}

// TestOSSCommand_PolicyStringsRejectsUnsplittableResource 覆盖 ParseOSSCommand 够不到的
// 那条路：OSSCommand 是导出结构体，可以被结构化参数直接构造、绕开解析期的 target 校验。
// 后置条件是那条缝上唯一的守卫，而这条缝破了是**静默**的——分不出两段时匹配器对任何
// 规则都返回 false，deny 规则一条都不匹配（fail-open）。
func TestOSSCommand_PolicyStringsRejectsUnsplittableResource(t *testing.T) {
	cases := []OSSCommand{
		{Family: "object", Verb: "stat", Target: " mybucket/key"},
		{Family: "object", Verb: "stat", Target: "mybucket/key "},
		{Family: "object", Verb: "stat", Target: "mybucket/key\u00a0"},
		{Family: "object", Verb: "delete", Target: ""},
		{Family: "object", Verb: "copy", Target: "src/a"},
		{Family: "object", Verb: "move", Target: "src/a", Flags: map[string]string{"to": " dst/b"}},
		{Family: "object", Verb: "presign", Target: "b/k", Flags: map[string]string{"method": "post"}},
		{Family: "object", Verb: "nonsense", Target: "b/k"},
	}
	for _, c := range cases {
		if got, err := c.PolicyStrings(); err == nil {
			t.Fatalf("PolicyStrings(%#v) = %q, nil error; want rejection (result would match no rule at all)", c, got)
		}
	}
}

// TestOSSCommand_PolicyStringsRejectsWholeBucketResource 是上一条在**语义**方向的同一件事：
// 解析期的 key 校验只守"解析命令串"这条路，而 OSSCommand 是导出结构体，统一 exec 会用
// 结构化参数直接构造它。派生串同时是 grant 规则，一个单对象操作派生出桶级/前缀级的
// resource 段，落库之后授权的是整桶（policy.MatchOSSRule 的空 key 语义，决策 D5）——
// 比切不出两段更糟：那个是 fail-open 到"匹配不上任何规则"，这个是 fail-open 到"匹配上全部"。
//
// `bucket list` 带 target 也在这里：解析期把它挡掉的理由是"批准一件事、拿到另一件"
// （审批弹窗照 Render 显示 `bucket list mybucket`，检查的却是 `bucket.list *`、执行器列出
// 全部桶）。结构体那条缝上同一件事一样会发生，而这里是它唯一的守卫。
func TestOSSCommand_PolicyStringsRejectsWholeBucketResource(t *testing.T) {
	cases := []OSSCommand{
		{Family: "object", Verb: "stat", Target: "mybucket"},
		{Family: "object", Verb: "get", Target: "mybucket/"},
		{Family: "object", Verb: "put", Target: "mybucket"},
		{Family: "object", Verb: "delete", Target: "mybucket/"},
		{Family: "object", Verb: "presign", Target: "mybucket"},
		{Family: "object", Verb: "copy", Target: "src/a.txt", Flags: map[string]string{"to": "dst"}},
		{Family: "object", Verb: "move", Target: "src/a.txt", Flags: map[string]string{"to": "dst/"}},
		{Family: "bucket", Verb: "list", Target: "mybucket"},
	}
	for _, c := range cases {
		if got, err := c.PolicyStrings(); err == nil {
			t.Fatalf("PolicyStrings(%#v) = %q, nil error; want rejection (a single-object operation must not derive a bucket-wide resource)", c, got)
		}
	}
}

// assertTwoSegmentOSSPolicyString 断言策略串能被"在第一段空白处切开"这条规则切成
// 恰好两段：action 不含空白，resource 非空且没有前导/尾随空白（interior 空白是允许的，
// 见决策 D4）。
func assertTwoSegmentOSSPolicyString(t *testing.T, in, s string) {
	t.Helper()
	action, resource, found := strings.Cut(s, " ")
	if !found || action == "" || strings.ContainsFunc(action, unicode.IsSpace) {
		t.Fatalf("policy string %q from %q has no <action> segment", s, in)
	}
	if resource == "" || strings.TrimSpace(resource) != resource {
		t.Fatalf("policy string %q from %q has a padded or empty <resource> segment", s, in)
	}
}
