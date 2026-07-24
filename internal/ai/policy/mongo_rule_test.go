package policy

import "testing"

// TestMatchMongoRule 锁住 op 感知匹配器的规则语法：`<operation> [collection-glob]`。
//
// 这个函数取代了 mongo 策略里的 policyValueMatches（裸 op 全等比较）。它必须同时满足
// 两组约束，任一破坏都是静默 fail-open：
//   - 存量兼容：内置组与存量 grant 里写的是裸 op（"find" / "dropCollection"），
//     命令侧过去也是裸 op；这些组合的判定必须与替换前逐一相同。
//   - 富命令串：统一 exec 送进来的是 `deleteMany users --db=prod --query=...`，
//     写成裸 op 的 deny 规则必须照样命中——这正是 MatchCommandRule 做不到、
//     从而把 deny 悄悄降级成 NeedConfirm 的地方。
func TestMatchMongoRule(t *testing.T) {
	cases := []struct {
		name    string
		rule    string
		command string
		want    bool
	}{
		// --- 裸 op（存量内置组的形状） ---
		{"bare op matches itself", "find", "find", true},
		{"bare op rejects another op", "find", "insertOne", false},
		{"deny op matches itself", "dropCollection", "dropCollection", true},

		// --- 裸 op 规则 × 富命令串（本任务要修的 fail-open） ---
		{"bare deny rule still matches rich command", "dropCollection", "dropCollection users", true},
		{"bare deny rule matches command with flags", "deleteMany",
			`deleteMany users --db=prod --query='{"filter":{"a":1}}'`, true},
		{"bare allow rule matches rich command", "find", `find users --query='{"filter":{}}'`, true},
		{"bare rule does not leak across ops", "find", `deleteMany users --db=prod`, false},

		// --- EqualFold：policyValueMatches 折叠大小写，替换后必须继续折叠 ---
		{"rule case folds against command", "DropDatabase", "dropDatabase", true},
		{"rule case folds against rich command", "DROPCOLLECTION", "dropCollection users", true},
		{"command case folds against rule", "dropdatabase", "DropDatabase", true},

		// --- 通配 ---
		{"star rule matches any command", "*", `deleteMany users --db=prod`, true},
		{"star rule rejects empty command", "*", "", false},
		{"star rule rejects blank command", "*", "   ", false},

		// --- collection 收窄 ---
		{"collection narrows rule", "deleteMany users", "deleteMany users", true},
		{"collection mismatch does not match", "deleteMany users", "deleteMany logs", false},
		{"collection glob matches", "deleteMany log*", "deleteMany logs_2026", true},
		{"collection glob rejects other collection", "deleteMany log*", "deleteMany users", false},
		{"collection rule ignores flags on command", "deleteMany users",
			`deleteMany users --db=prod --query='{"filter":{}}'`, true},
		{"collection rule needs a collection on the command", "deleteMany users", "deleteMany", false},
		{"star collection matches any collection", "deleteMany *", "deleteMany users", true},
		{"star collection matches a bare op command", "deleteMany *", "deleteMany", true},
		{"collection match is case sensitive", "deleteMany Users", "deleteMany users", false},

		// --- 引号感知：带空格的 collection 是一个 token，不是两个 ---
		{"quoted collection stays one token", "deleteMany 'my coll'", `deleteMany 'my coll'`, true},
		{"quoted collection does not match its first word", "deleteMany my", `deleteMany 'my coll'`, false},

		// --- 规则里的 flag 不参与匹配（收窄只有 collection 一维） ---
		{"flags in rule are ignored", "listCollections --db=analytics", "listCollections --db=other", true},
		{"grant pattern ignores db and query narrowing", `deleteMany logs --db=prod --query='{"filter":{"a":1}}'`,
			`deleteMany logs --db=stage --query='{"filter":{"b":2}}'`, true},

		// --- 空/畸形输入 ---
		{"empty rule matches nothing", "", "find", false},
		{"blank rule matches nothing", "   ", "find", false},
		{"empty command matches nothing", "find", "", false},
		{"unterminated quote on command still matches its op", "deleteMany", "deleteMany 'users", true},
		{"unterminated quote on rule falls back to fields", "deleteMany 'users", "deleteMany 'users", true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := MatchMongoRule(c.rule, c.command); got != c.want {
				t.Fatalf("MatchMongoRule(%q, %q) = %v, want %v", c.rule, c.command, got, c.want)
			}
		})
	}
}

// TestMatchMongoRule_MatchesPolicyValueMatchesOnBareOps 是存量兼容的机械证明：
// 对"规则与命令都是裸 op"的全部组合，新匹配器必须与它替换掉的 policyValueMatches
// 给出完全相同的答案。这层组合正是内置组（BuiltinMongoReadOnly /
// BuiltinMongoDangerousDeny）唯一会产生的形状。
func TestMatchMongoRule_MatchesPolicyValueMatchesOnBareOps(t *testing.T) {
	ops := []string{
		"find", "findOne", "insertOne", "insertMany", "updateOne", "updateMany",
		"deleteOne", "deleteMany", "aggregate", "countDocuments",
		"listDatabases", "listCollections", "dropDatabase", "dropCollection",
		"DropDatabase", "DROPCOLLECTION", "*",
	}
	for _, rule := range ops {
		for _, op := range ops {
			if op == "*" {
				// "*" 作为命令没有意义（不是任何 op），两个函数在这里本就不同：
				// policyValueMatches 走的是"规则与值全等"的字符串比较。
				continue
			}
			want := policyValueMatches(rule, op)
			if got := MatchMongoRule(rule, op); got != want {
				t.Fatalf("MatchMongoRule(%q, %q) = %v, want %v (policyValueMatches parity)", rule, op, got, want)
			}
		}
	}
}
