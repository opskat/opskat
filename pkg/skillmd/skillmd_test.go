package skillmd

import "testing"

func TestParse(t *testing.T) {
	raw := "---\nname: ssh\ndescription: \"Run shell commands over SSH.\"\n---\n\n# SSH\n\nbody text\n"
	s, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if s.Name != "ssh" {
		t.Errorf("Name = %q, want ssh", s.Name)
	}
	if s.Description != "Run shell commands over SSH." {
		t.Errorf("Description = %q", s.Description)
	}
	if s.Body != "# SSH\n\nbody text\n" {
		t.Errorf("Body = %q", s.Body)
	}
}

func TestParse_CRLF(t *testing.T) {
	raw := "---\r\nname: x\r\ndescription: d\r\n---\r\nbody\r\n"
	if _, err := Parse(raw); err != nil {
		t.Fatalf("CRLF input must parse: %v", err)
	}
}

func TestParse_Rejects(t *testing.T) {
	cases := map[string]string{
		"no frontmatter": "# Just a heading\n",
		"unterminated":   "---\nname: x\n",
		"no description": "---\nname: x\n---\nbody\n",
		"empty":          "",
	}
	for label, raw := range cases {
		if _, err := Parse(raw); err == nil {
			t.Errorf("%s: expected an error, got nil", label)
		}
	}
}

// name 缺失是允许的（内置 skill 的目录名/扩展名才是权威标识），
// description 缺失不允许——它是 prompt 里那份清单的唯一内容来源。
func TestParse_NameIsOptional(t *testing.T) {
	if _, err := Parse("---\ndescription: d\n---\nbody\n"); err != nil {
		t.Errorf("missing name must be tolerated: %v", err)
	}
}
