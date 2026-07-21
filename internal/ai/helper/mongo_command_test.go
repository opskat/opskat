package helper

import "testing"

func TestParseMongoCommand(t *testing.T) {
	cases := []struct {
		in   string
		want MongoCommand
	}{
		{`find users`, MongoCommand{Op: "find", Collection: "users"}},
		{`find users --query='{"filter":{"age":{"$gt":21}}}'`,
			MongoCommand{Op: "find", Collection: "users", Query: `{"filter":{"age":{"$gt":21}}}`}},
		{`find users --db=analytics`,
			MongoCommand{Op: "find", Collection: "users", Database: "analytics"}},
		{`listDatabases`, MongoCommand{Op: "listDatabases"}},
		{`deleteMany logs --query='{"filter":{"level":"debug"}}'`,
			MongoCommand{Op: "deleteMany", Collection: "logs", Query: `{"filter":{"level":"debug"}}`}},
	}
	for _, c := range cases {
		got, err := ParseMongoCommand(c.in)
		if err != nil {
			t.Fatalf("ParseMongoCommand(%q) unexpected error: %v", c.in, err)
		}
		if *got != c.want {
			t.Fatalf("ParseMongoCommand(%q) = %#v, want %#v", c.in, *got, c.want)
		}
	}
}

func TestParseMongoCommand_RejectsUnknownOp(t *testing.T) {
	if _, err := ParseMongoCommand("dropEverything users"); err == nil {
		t.Fatal("ParseMongoCommand with unknown op = nil error, want rejection")
	}
}

func TestParseMongoCommand_RequiresCollectionWhereApplicable(t *testing.T) {
	if _, err := ParseMongoCommand("find"); err == nil {
		t.Fatal("ParseMongoCommand(\"find\") = nil error, want rejection (collection required)")
	}
}

func TestMongoCommand_RoundTrip(t *testing.T) {
	cmds := []MongoCommand{
		{Op: "find", Collection: "users"},
		{Op: "find", Collection: "users", Database: "analytics"},
		{Op: "find", Collection: "users", Query: `{"filter":{"a":1}}`},
		{Op: "aggregate", Collection: "events", Query: `{"pipeline":[{"$match":{"x":"a b"}}]}`},
		{Op: "insertOne", Collection: "users", Query: `{"document":{"name":"o'brien"}}`},
		{Op: "listDatabases"},
		{Op: "listCollections", Database: "analytics"},
	}
	for _, want := range cmds {
		rendered := want.Render()
		got, err := ParseMongoCommand(rendered)
		if err != nil {
			t.Fatalf("ParseMongoCommand(Render(%#v)) = error %v (rendered: %s)", want, err, rendered)
		}
		if *got != want {
			t.Fatalf("round-trip = %#v, want %#v (rendered: %s)", *got, want, rendered)
		}
	}
}

// TestMongoCommand_PolicyStringIsBareOp 锁住最要紧的兼容性约束：策略串必须仍是
// 裸 operation token。改它 = BuiltinMongoReadOnly 的 AllowTypes 与全部存量 grant 静默失配。
func TestMongoCommand_PolicyStringIsBareOp(t *testing.T) {
	c := MongoCommand{Op: "find", Collection: "users", Database: "analytics", Query: `{"filter":{"a":1}}`}
	if got := c.PolicyString(); got != "find" {
		t.Fatalf("PolicyString() = %q, want %q — changing this breaks BuiltinMongoReadOnly and stored grants", got, "find")
	}
}
