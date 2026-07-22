package skills

import (
	"strings"
	"testing"
)

func TestGet_AllRegisteredTypesPresent(t *testing.T) {
	for _, at := range []string{"ssh", "serial", "database", "redis", "k8s", "etcd", "mongodb"} {
		body, ok := Get(at)
		if !ok {
			t.Fatalf("no SKILL.md registered for %q", at)
		}
		if !strings.Contains(body, "## Command syntax") {
			t.Fatalf("%s SKILL.md missing '## Command syntax' section", at)
		}
	}
}

func TestDescription_ParsedFromFrontmatter(t *testing.T) {
	desc, ok := Description("redis")
	if !ok {
		t.Fatal("no description for redis")
	}
	if desc == "" {
		t.Fatal("redis description is empty")
	}
	if strings.Contains(desc, "---") {
		t.Fatalf("description still contains frontmatter delimiters: %q", desc)
	}
}

func TestGet_BodyExcludesFrontmatter(t *testing.T) {
	body, _ := Get("ssh")
	if strings.HasPrefix(strings.TrimSpace(body), "---") {
		t.Fatal("Get should return the body without frontmatter")
	}
}

func TestTypes_Sorted(t *testing.T) {
	got := Types()
	// 8 exec types (ssh/serial/database/redis/k8s/etcd/mongodb/kafka) + 4 doc-only
	// types (rdp/vnc/oss/local) registered via RegisterHelpDoc — see execimpl/register.go.
	if len(got) != 12 {
		t.Fatalf("got %d types, want 12", len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i-1] > got[i] {
			t.Fatalf("Types not sorted: %v", got)
		}
	}
}

func TestGet_UnknownType(t *testing.T) {
	// "bogus" is not a registered asset type and never will be, so it has no SKILL.md.
	// (vnc used to stand in here; it now has a doc-only SKILL.md — see internal/ai/skills/vnc.)
	if _, ok := Get("bogus"); ok {
		t.Fatal("bogus is not a real asset type; Get should report it as unknown")
	}
}
