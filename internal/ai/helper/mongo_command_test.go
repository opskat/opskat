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

// TestParseMongoCommand_RejectsStrayPositionalForCollectionlessOps guards MINOR 1:
// a model reading `<op> [collection]` could plausibly write `listCollections mydb`
// expecting mydb to be the database. It parses to Collection="mydb", Database=""
// and dies downstream with a confusing "database 参数不能为空". Reject it up front
// with a message that says why.
func TestParseMongoCommand_RejectsStrayPositionalForCollectionlessOps(t *testing.T) {
	cases := []string{"listDatabases extra", "listCollections mydb"}
	for _, in := range cases {
		if _, err := ParseMongoCommand(in); err == nil {
			t.Fatalf("ParseMongoCommand(%q) = nil error, want rejection (op takes no collection)", in)
		}
	}
}

// TestParseMongoCommand_UnknownFlagErrorIsDeterministic guards MINOR 2: parsed.Flags
// is a Go map, so naively reporting "the first unknown flag" would name a random one
// of several unknown flags on different runs. The error text goes to the model, so it
// must be the same every time for the same input.
func TestParseMongoCommand_UnknownFlagErrorIsDeterministic(t *testing.T) {
	const want = `unknown flag(s) --a, --b; mongo supports --db and --query`
	for i := 0; i < 20; i++ {
		_, err := ParseMongoCommand("find u --a=1 --b=2")
		if err == nil {
			t.Fatal("ParseMongoCommand with unknown flags = nil error, want rejection")
		}
		if err.Error() != want {
			t.Fatalf("run %d: ParseMongoCommand error = %q, want %q", i, err.Error(), want)
		}
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
		rendered, err := want.Render()
		if err != nil {
			t.Fatalf("Render(%#v) unexpected error: %v", want, err)
		}
		got, err := ParseMongoCommand(rendered)
		if err != nil {
			t.Fatalf("ParseMongoCommand(Render(%#v)) = error %v (rendered: %s)", want, err, rendered)
		}
		if *got != want {
			t.Fatalf("round-trip = %#v, want %#v (rendered: %s)", *got, want, rendered)
		}
	}
}

// TestMongoCommand_RenderRejectsFlagLikeCollection guards MINOR 3: Collection is
// exported, so a caller can build a MongoCommand directly (e.g. from the unified
// exec tool's structured "collection" argument) without going through
// ParseMongoCommand, and hand it a value ParseMongoCommand could never produce.
// cmdline.Parse decides positional vs. flag purely by whether the (already
// unquoted) word starts with "--", so quoting can't make such a value round-trip:
// Render must refuse instead of silently producing a string that reparses to a
// different MongoCommand (or fails to parse at all).
func TestMongoCommand_RenderRejectsFlagLikeCollection(t *testing.T) {
	cases := []MongoCommand{
		{Op: "find", Collection: "--db=evil"},
		{Op: "find", Collection: "--"},
	}
	for _, c := range cases {
		if _, err := c.Render(); err == nil {
			t.Fatalf("Render(%#v) = nil error, want rejection (collection looks like a flag)", c)
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
