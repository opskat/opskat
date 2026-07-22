package skillmd

import (
	"errors"
	"testing"
)

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

// TestParse_AttemptedFrontmatterHardFailsInsteadOfDegrading covers input that
// clearly *tried* to open a frontmatter block but doesn't match the strict
// "---\n" prefix this parser requires. Before this test, both inputs slipped
// past the exact 4-byte prefix check and were misclassified as ErrNoFrontmatter
// (the sentinel that means "no frontmatter was attempted at all"). At the
// pkg/extension boundary that sentinel triggers a *tolerate* branch -- the
// whole document, frontmatter block included, becomes the body and gets
// injected into the system prompt verbatim as noise. A near-miss frontmatter
// block must instead hard-fail so the authoring mistake is surfaced loudly.
func TestParse_AttemptedFrontmatterHardFailsInsteadOfDegrading(t *testing.T) {
	cases := map[string]string{
		"leading blank line before the opening delimiter": "\n---\nname: my-ext\ndescription: \"does things\"\n---\n\n# body\n",
		"trailing whitespace after the opening delimiter": "---  \nname: x\ndescription: d\n---\nbody\n",
	}
	for label, raw := range cases {
		_, err := Parse(raw)
		if err == nil {
			t.Errorf("%s: expected a hard failure, got nil", label)
			continue
		}
		if errors.Is(err, ErrNoFrontmatter) {
			t.Errorf("%s: must not degrade to ErrNoFrontmatter, got: %v", label, err)
		}
	}
}
