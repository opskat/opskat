package skills

import (
	"strings"
	"testing"
)

func TestGet_AllRegisteredTypesPresent(t *testing.T) {
	for _, at := range []string{"ssh", "serial", "database", "redis", "k8s", "etcd"} {
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
	if len(got) != 6 {
		t.Fatalf("got %d types, want 6", len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i-1] > got[i] {
			t.Fatalf("Types not sorted: %v", got)
		}
	}
}

func TestGet_UnknownType(t *testing.T) {
	if _, ok := Get("mongodb"); ok {
		t.Fatal("mongodb should not be registered in Plan A")
	}
}
