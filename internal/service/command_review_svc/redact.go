package command_review_svc

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// Syntax 是命令的写法，决定怎样在里面找敏感信息。资产类型在注册权限检查时声明自己的写法。
type Syntax string

const (
	// SyntaxText 是 SQL、路径、扩展类型的命令等：只按值本身的格式和紧挨着的键名找。
	SyntaxText Syntax = ""
	// SyntaxShell 是 shell 命令（SSH、串口、k8s），以及按 shell 词法切词的 etcd / mongo / kafka 命令。
	SyntaxShell Syntax = "shell"
	// SyntaxRedis 是 Redis 命令。
	SyntaxRedis Syntax = "redis"
)

// ErrUnparseable 表示命令解析不了，分不清哪些是值、哪些是命令结构，所以不能发送。
var ErrUnparseable = errors.New("command cannot be parsed")

const mask = "***"

// RedactSensitive 把命令里的敏感信息（密码、token、密钥等）换成 ***，再发给外部模型审核。
//
// 只换值本身，不动命令结构：命令名、分隔符、管道、重定向，以及 $(…)、反引号里要执行的
// 内容都原样保留，否则模型看不到危险的部分。值里带命令替换时也不换——宁可把这个值发出去，
// 也不能把要执行的代码藏起来。shell 命令解析不了时返回 ErrUnparseable。
func RedactSensitive(syn Syntax, command string) (string, error) {
	switch syn {
	case SyntaxShell:
		return redactShell(command)
	case SyntaxRedis:
		return redactRedis(command), nil
	default:
		return redactText(command), nil
	}
}

// --- 按值的格式找（所有写法共用） ---

// valueRule 找出一种敏感值：re 的第 group 个捕获组就是这个值（0 表示整个匹配），只换它。
type valueRule struct {
	re    *regexp.Regexp
	group int
	// skip 排除看起来像、其实不是敏感值的匹配，可为 nil。
	skip func(match, value string) bool
}

// 名字里带这些词的变量、字段、参数，它的值当作敏感值。
const (
	sensitiveName = `(?:password|passwd|passphrase|pwd|token|secret|credentials?|(?:api|access|account|private|auth|secret)[-_]?key)`
	sensitiveFlag = `(?:password|passwd|pass|pwd|passphrase|token|secret|credentials?|(?:api|access|private|auth|secret)[-_]?key|keys?)`
	// keyName 是名字最后一段叫 key 的变量、参数（ENCRYPTION_KEY、--encryption-key）：
	// 按整段认，monkey、keyboard、KEYCLOAK_URL 不算；KEY_ID、KEY_DIR 这类后面还有一段的也不算。
	keyName = `(?:[a-z0-9]+[-_])*keys?`
)

// locationNameRe 认出表示文件位置的名字（MYSQL_PASSWORD_FILE、TOKEN_PATH、KEY_DIR）：值是路径，
// 不是密钥。不换它——`KEY_DIR=/ rm -rf $KEY_DIR/*` 里换掉 / 就藏起了删的是哪里。
var locationNameRe = regexp.MustCompile(`(?i)[-_](?:file|path|dir)s?$`)

// leadingName 取出匹配开头的名字（字段名、变量名），给 skipLocationName 用。
var leadingName = regexp.MustCompile(`^[A-Za-z0-9_-]+`)

func skipLocationName(match, _ string) bool {
	return locationNameRe.MatchString(leadingName.FindString(match))
}

// textRules 按值的格式和紧挨着的键名找敏感值。redirect 是裸值还要在哪些字符前结束：shell 命令里
// 是 "<>"——重定向是命令结构，要留给模型看；SQL 等不经过 shell 的纯文本里为空，< > 可以是值的一部分。
func textRules(redirect string) []valueRule {
	// argValue 是跟在参数后面的一个值：引号括起来的，或到空白 / shell 分隔符为止、不以 - 开头的裸值。
	argValue := `('[^']*'|"[^"]*"|[^\s\-;&|'"` + redirect + `][^\s;&|'"` + redirect + `]*)`
	// attachedValue 是紧贴在 -p 后面的值。
	attachedValue := `('[^']*'|"[^"]*"|[^\s;&|'"` + redirect + `]+)`
	return append([]valueRule{
		// 私钥：只换 BEGIN / END 之间的内容。
		{re: regexp.MustCompile(`(?s)-----BEGIN [A-Z0-9 ]*PRIVATE KEY(?: BLOCK)?-----(.+?)-----END [A-Z0-9 ]*PRIVATE KEY(?: BLOCK)?-----`), group: 1},
		// 网址里的 用户名:密码@。
		{re: regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.\-]*://[^\s:/@'"]*:([^\s@/'"]+)@`), group: 1},
		// 请求头。
		{re: regexp.MustCompile(`(?i)\bauthorization:\s*(?:(?:bearer|basic|token|digest)\s+)?([^\s'"` + redirect + `]+)`), group: 1, skip: isAuthScheme},
		{re: regexp.MustCompile(`(?i)\b(?:x-api-key|x-auth-token|api-key|private-token):\s*([^\s'"` + redirect + `]+)`), group: 1},
		// JSON / JS 对象 / YAML 里的字段："password": "…"、pwd: '…'。
		{re: regexp.MustCompile(`(?i)\b[a-z0-9_-]*` + sensitiveName + `[a-z0-9_-]*["']?\s*:\s*"([^"]*)"`), group: 1, skip: skipLocationName},
		{re: regexp.MustCompile(`(?i)\b[a-z0-9_-]*` + sensitiveName + `[a-z0-9_-]*["']?\s*:\s*'([^']*)'`), group: 1, skip: skipLocationName},
		// SQL 里设置密码。
		{re: regexp.MustCompile(`(?i)\bIDENTIFIED\s+(?:WITH\s+\S+\s+)?BY\s+'([^']*)'`), group: 1},
		{re: regexp.MustCompile(`(?i)\bIDENTIFIED\s+(?:WITH\s+\S+\s+)?BY\s+"([^"]*)"`), group: 1},
		{re: regexp.MustCompile(`(?i)\bPASSWORD\s+'([^']*)'`), group: 1},
		// 命令行参数。shell 命令本身按解析结果处理（见 argEdits），这几条管的是引号里的远端命令等
		// 解析器看不进去的地方。
		{re: regexp.MustCompile(`(?i)\b(?:mysql|mysqldump|mysqladmin|mariadb|mariadb-dump)\b[^;&|\n]*?\s-p` + attachedValue), group: 1},
		{re: regexp.MustCompile(`\bsshpass\s+-p\s*` + attachedValue), group: 1},
		{re: regexp.MustCompile(`\bredis-cli\b[^;&|\n]*?\s-a\s+` + argValue), group: 1},
		{re: regexp.MustCompile(`\bcurl\b[^;&|\n]*?\s(?:-u|--user)\s+[^\s:'"/]+:([^\s'"@/` + redirect + `]+)`), group: 1},
		{re: regexp.MustCompile(`(?i)(?:^|\s)(--?(?:[a-z0-9]+[-_])*` + sensitiveFlag + `)\s+` + argValue), group: 2, skip: isNegatedFlag},
		// NAME=值：变量名、参数名里带 password / token / secret / key 等，也覆盖 --password=值 和网址参数。
		{re: regexp.MustCompile(`(?i)\b(?:[a-z0-9_-]*` + sensitiveName + `[a-z0-9_-]*|` + keyName + `)=('[^']*'|"[^"]*"|[^\s;&|'"` + redirect + `]+)`), group: 1, skip: skipLocationName},
	}, knownFormats()...)
}

var (
	plainTextRules = textRules("")
	shellTextRules = textRules("<>")
)

// knownFormats 是常见服务的密钥格式，取自 betterleaks（MIT，github.com/betterleaks/betterleaks）
// v1.9.0 的 config/betterleaks.toml：只选带固定前缀、不需要上下文就能认出来的格式，
// 去掉了原规则结尾的边界判断。sk- 那条是本项目自己的，覆盖 OpenAI、Anthropic、DeepSeek 等
// 同一前缀的密钥。
func knownFormats() []valueRule {
	formats := []string{
		`\b((?:A3T[A-Z0-9]|AKIA|ASIA|ABIA|ACCA)[A-Z2-7]{16})\b`,                                           // AWS
		`\b(gh[pousr]_[0-9a-zA-Z]{36}|github_pat_\w{82})\b`,                                               // GitHub
		`\b(gl(?:pat|rt|dt)-[\w-]{20,})`,                                                                  // GitLab
		`\b(xoxb-[0-9]{10,13}-[0-9]{10,13}[a-zA-Z0-9-]*|xox[pe](?:-[0-9]{10,13}){3}-[a-zA-Z0-9-]{28,34})`, // Slack
		`hooks\.slack\.com/(?:services|workflows|triggers)/([A-Za-z0-9+/]{43,56})`,                        // Slack webhook
		`\b((?:sk|rk)_(?:test|live|prod)_[a-zA-Z0-9]{10,99})\b`,                                           // Stripe
		`\b(sk-[A-Za-z0-9_\-]{16,})`,                                                                      // OpenAI 等
		`\b(AIza[\w-]{35})`,                                                                               // Google Cloud
		`\b(dckr_(?:pat|oat)_[A-Za-z0-9_-]{27,32})`,                                                       // Docker Hub
		`(?i)\b(npm_[a-z0-9]{36})\b`,                                                                      // npm
		`(pypi-AgEIcHlwaS5vcmc[\w-]{50,1000})`,                                                            // PyPI
		`\b(ey[a-zA-Z0-9]{17,}\.ey[a-zA-Z0-9/\\_-]{17,}\.(?:[a-zA-Z0-9/\\_-]{10,}={0,2})?)`,               // JWT
		`\b(hvs\.[\w-]{90,120})`,                                                                          // HashiCorp Vault
		`\b(dop_v1_[a-f0-9]{64})\b`,                                                                       // DigitalOcean
		`\b(SG\.(?i:[a-z0-9=_\-.]{66}))`,                                                                  // SendGrid
		`\b(hf_[a-zA-Z]{34})\b`,                                                                           // Hugging Face
		`\b(LTAI[A-Za-z0-9]{17,21})\b`,                                                                    // 阿里云
		`(AGE-SECRET-KEY-1[QPZRY9X8GF2TVDW0S3JN54KHCE6MUA7L]{58})`,                                        // age
		`\b(glc_[A-Za-z0-9+/]{40,150}={0,2}|glsa_[A-Za-z0-9]{32}_[A-Fa-f0-9]{8})`,                         // Grafana
		`\b(SWMTKN-1-[a-z0-9]{50,60}-[a-z0-9]{24,30}|SWMKEY-1-[A-Za-z0-9+/]{40,50})`,                      // Docker Swarm
		`\b(pul-[a-f0-9]{40})\b`,                                                                          // Pulumi
		`(dp\.pt\.(?i:[a-z0-9]{43}))`,                                                                     // Doppler
		`\b(sntryu_[a-f0-9]{64})\b`,                                                                       // Sentry
		`(?i)\b(gsk_[a-z0-9]{52})\b`,                                                                      // Groq
		`(?i)\b(xai-[a-z0-9_-]{70,120})`,                                                                  // xAI
		`\b(ATAT[A-Za-z0-9_\-=]{100,})`,                                                                   // Atlassian
		`(shpat_[a-fA-F0-9]{32})`,                                                                         // Shopify
	}
	rules := make([]valueRule, len(formats))
	for i, f := range formats {
		rules[i] = valueRule{re: regexp.MustCompile(f), group: 1}
	}
	return rules
}

func isAuthScheme(_, value string) bool {
	switch strings.ToLower(value) {
	case "bearer", "basic", "token", "digest":
		return true
	}
	return false
}

// isNegatedFlag 排除 --no-password 这类不带值的开关：它后面的词不是密码。
func isNegatedFlag(match, _ string) bool {
	flag := strings.ToLower(strings.TrimSpace(match))
	return strings.HasPrefix(flag, "--no-") || strings.HasPrefix(flag, "-no-")
}

// runsCode 判断值里有没有会被执行的内容（命令替换、进程替换）。
func runsCode(v string) bool {
	return strings.Contains(v, "$(") || strings.Contains(v, "`") || strings.Contains(v, "<(") || strings.Contains(v, ">(")
}

func (r valueRule) apply(s string) string {
	matches := r.re.FindAllStringSubmatchIndex(s, -1)
	if matches == nil {
		return s
	}
	var edits []edit
	for _, m := range matches {
		start, end := m[2*r.group], m[2*r.group+1]
		if start < 0 || start == end {
			continue
		}
		v := s[start:end]
		if v == mask || runsCode(v) || (r.skip != nil && r.skip(s[m[0]:m[1]], v)) {
			continue
		}
		edits = append(edits, edit{start: start, end: end, text: mask})
	}
	return applyEdits(s, edits)
}

// redactText 在不经过 shell 的纯文本里找敏感值（见 textRules）。
func redactText(s string) string { return redactWith(plainTextRules, s) }

// redactWith 按顺序执行规则，每条规则只换一个值。
func redactWith(rules []valueRule, s string) string {
	for _, r := range rules {
		s = r.apply(s)
	}
	return s
}

// --- shell ---

// 这些程序的某些参数后面跟的是密码。ssh 等程序没有这样的参数，列出来是为了
// 让 `sshpass -p pw ssh -p 22 host` 里 ssh 的 -p 不再按 sshpass 的规则处理。
var programs = map[string]bool{
	"mysql": true, "mysqldump": true, "mysqladmin": true, "mariadb": true, "mariadb-dump": true,
	"sshpass": true, "redis-cli": true, "curl": true,
	"ssh": true, "scp": true, "rsync": true,
}

var mysqlPrograms = map[string]bool{"mysql": true, "mysqldump": true, "mysqladmin": true, "mariadb": true, "mariadb-dump": true}

var (
	sensitiveFlagRe = regexp.MustCompile(`(?i)^--?(?:[a-z0-9]+[-_])*` + sensitiveFlag + `$`)
	sensitiveNameRe = regexp.MustCompile(`(?i)^(?:[a-z0-9_]*` + sensitiveName + `[a-z0-9_]*|` + keyName + `)$`)
)

// isSensitiveName 判断变量名、设置名的值是不是敏感值；表示文件位置的名字不算（见 locationNameRe）。
func isSensitiveName(name string) bool {
	return sensitiveNameRe.MatchString(name) && !locationNameRe.MatchString(name)
}

// isSensitiveSetting 判断作为单独参数出现、后面紧跟着值的设置名（aws configure set、redis CONFIG SET）。
func isSensitiveSetting(word string) bool {
	switch strings.ToLower(word) {
	case "requirepass", "masterauth":
		return true
	}
	return strings.Contains(word, "_") && isSensitiveName(word)
}

func isSensitiveFlag(word string) bool {
	return sensitiveFlagRe.MatchString(word) && !isNegatedFlag(word, "")
}

// redactShell 先按命令结构找值（敏感变量的赋值、敏感参数后面的值），再在剩下的字面量
// （引号里的内容、heredoc、注释）里按值的格式找。只换完全是字面量的值。
func redactShell(command string) (string, error) {
	file, err := syntax.NewParser(syntax.KeepComments(true)).Parse(strings.NewReader(command), "")
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrUnparseable, err)
	}
	var edits []edit
	syntax.Walk(file, func(n syntax.Node) bool {
		switch x := n.(type) {
		case *syntax.Assign:
			if x.Name != nil && x.Value != nil && isSensitiveName(x.Name.Value) {
				if _, ok := literalValue(x.Value); ok {
					edits = append(edits, maskNode(x.Value))
				}
			}
		case *syntax.CallExpr:
			edits = append(edits, argEdits(x.Args)...)
		}
		return true
	})
	var textEdits []edit
	syntax.Walk(file, func(n syntax.Node) bool {
		switch n.(type) {
		case *syntax.Lit, *syntax.SglQuoted, *syntax.Comment:
			start, end := int(n.Pos().Offset()), int(n.End().Offset())
			if overlaps(edits, start, end) {
				return true
			}
			// 引号里的内容可能是远端要执行的命令（ssh web '…'、bash -c "…"），按 shell 的写法找。
			if s := redactWith(shellTextRules, command[start:end]); s != command[start:end] {
				textEdits = append(textEdits, edit{start: start, end: end, text: s})
			}
		}
		return true
	})
	return applyEdits(command, append(edits, textEdits...)), nil
}

// argEdits 找出一条简单命令里敏感参数后面的值。
func argEdits(args []*syntax.Word) []edit {
	var edits []edit
	prog := ""
	// valueAt 返回第 i 个参数：它必须完全是字面量，并且不像另一个参数（不以 - 开头）。
	valueAt := func(i int) (string, bool) {
		if i >= len(args) {
			return "", false
		}
		v, ok := literalValue(args[i])
		return v, ok && v != "" && !strings.HasPrefix(v, "-")
	}
	for i, w := range args {
		word, ok := literalValue(w)
		if !ok {
			continue
		}
		if programs[path.Base(word)] {
			prog = path.Base(word)
			continue
		}
		switch {
		case isSensitiveFlag(word), isSensitiveSetting(word),
			prog == "redis-cli" && word == "-a",
			prog == "sshpass" && word == "-p":
			if _, ok := valueAt(i + 1); ok {
				edits = append(edits, maskNode(args[i+1]))
			}
		case prog == "curl" && (word == "-u" || word == "--user"):
			// 只换冒号后面的密码，用户名留着。
			if v, ok := valueAt(i + 1); ok {
				if user, _, found := strings.Cut(v, ":"); found {
					edits = append(edits, edit{start: int(args[i+1].Pos().Offset()), end: int(args[i+1].End().Offset()), text: user + ":" + mask})
				}
			}
		case (mysqlPrograms[prog] || prog == "sshpass") && strings.HasPrefix(word, "-p") && len(word) > 2:
			edits = append(edits, edit{start: int(w.Pos().Offset()) + 2, end: int(w.End().Offset()), text: mask})
		}
	}
	return edits
}

// literalValue 返回词去掉引号后的值；词里有变量、命令替换等展开时 ok 为 false。
func literalValue(w *syntax.Word) (string, bool) {
	var b strings.Builder
	for _, part := range w.Parts {
		switch p := part.(type) {
		case *syntax.Lit:
			b.WriteString(p.Value)
		case *syntax.SglQuoted:
			b.WriteString(p.Value)
		case *syntax.DblQuoted:
			for _, q := range p.Parts {
				lit, ok := q.(*syntax.Lit)
				if !ok {
					return "", false
				}
				b.WriteString(lit.Value)
			}
		default:
			return "", false
		}
	}
	return b.String(), true
}

func maskNode(n syntax.Node) edit {
	return edit{start: int(n.Pos().Offset()), end: int(n.End().Offset()), text: mask}
}

// --- Redis ---

// redisToken 是 Redis 命令里的一个词：start / end 是它在原文里的位置，value 是去掉引号后的值。
type redisToken struct {
	start, end int
	value      string
}

// redactRedis 按 Redis 命令的写法换掉密码参数，再按值的格式找其余的敏感值。
func redactRedis(command string) string {
	toks := redisTokens(command)
	if len(toks) == 0 {
		return command
	}
	var edits []edit
	maskAt := func(i int) {
		if i < len(toks) {
			edits = append(edits, edit{start: toks[i].start, end: toks[i].end, text: mask})
		}
	}
	switch strings.ToUpper(toks[0].value) {
	case "AUTH": // AUTH [用户名] 密码
		maskAt(len(toks) - 1)
	case "HELLO", "MIGRATE": // HELLO 3 AUTH 用户名 密码；MIGRATE … AUTH 密码 / AUTH2 用户名 密码
		for i, t := range toks {
			switch strings.ToUpper(t.value) {
			case "AUTH":
				if strings.EqualFold(toks[0].value, "HELLO") {
					maskAt(i + 2)
				} else {
					maskAt(i + 1)
				}
			case "AUTH2":
				maskAt(i + 2)
			}
		}
	case "CONFIG": // CONFIG SET 名字 值 [名字 值 …]
		if len(toks) > 1 && strings.EqualFold(toks[1].value, "SET") {
			for i := 2; i+1 < len(toks); i += 2 {
				if isSensitiveSetting(toks[i].value) {
					maskAt(i + 1)
				}
			}
		}
	case "ACL": // ACL SETUSER 用户名 >密码 <密码 #哈希 !哈希 …
		if len(toks) > 3 && strings.EqualFold(toks[1].value, "SETUSER") {
			for _, t := range toks[3:] {
				if v := t.value; v != "" && strings.ContainsRune("><#!", rune(v[0])) && len(v) > 1 {
					edits = append(edits, edit{start: t.start, end: t.end, text: v[:1] + mask})
				}
			}
		}
	}
	return redactText(applyEdits(command, edits))
}

// redisTokens 按空白切词，引号括起来的部分算一个词。
func redisTokens(s string) []redisToken {
	var toks []redisToken
	i := 0
	for {
		for i < len(s) && isSpace(s[i]) {
			i++
		}
		if i >= len(s) {
			return toks
		}
		start := i
		var b strings.Builder
		for i < len(s) && !isSpace(s[i]) {
			if q := s[i]; q == '"' || q == '\'' {
				j := i + 1
				for j < len(s) && s[j] != q {
					if q == '"' && s[j] == '\\' {
						j++
					}
					j++
				}
				b.WriteString(s[i+1 : min(j, len(s))])
				i = min(j+1, len(s))
				continue
			}
			b.WriteByte(s[i])
			i++
		}
		toks = append(toks, redisToken{start: start, end: i, value: b.String()})
	}
}

func isSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }

// --- 替换 ---

// edit 把原文 [start, end) 换成 text。
type edit struct {
	start, end int
	text       string
}

func overlaps(edits []edit, start, end int) bool {
	for _, e := range edits {
		if e.start < end && start < e.end {
			return true
		}
	}
	return false
}

// applyEdits 按位置替换；和前一处重叠的替换跳过。
func applyEdits(s string, edits []edit) string {
	if len(edits) == 0 {
		return s
	}
	sort.Slice(edits, func(i, j int) bool { return edits[i].start < edits[j].start })
	var b strings.Builder
	last := 0
	for _, e := range edits {
		if e.start < last {
			continue
		}
		b.WriteString(s[last:e.start])
		b.WriteString(e.text)
		last = e.end
	}
	b.WriteString(s[last:])
	return b.String()
}
