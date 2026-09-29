package extension

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// decodePolicyDecision reads a check_policy reply into the action and the
// resources the call touches, each as a path.Match glob — the one form the host
// judges resources in (internal/ai/policy: a deny hits a resource when its glob
// may match one of the resource's names, an allow or grant covers it only when
// it matches all of them).
//
// The reply's shape says how literally to take a resource:
//
//   - {"action","resource"} (2.0/2.1, Policy/Resource and PolicyFunc) names one
//     literal resource. Every glob character in it is quoted, so it stands for
//     exactly itself and the host matches it exactly as it always has — a
//     resource "prod-*" is the one resource called that, not every prod- name.
//     An empty resource is no resource.
//   - {"action","resources"} (2.2, PolicyResources) is a list whose '*' / '?'
//     are wildcards; '\', '[' and ']' are quoted so nothing else is glob syntax.
func decodePolicyDecision(raw []byte) (action string, resources []string, err error) {
	var decision struct {
		Action    string    `json:"action"`
		Resource  string    `json:"resource"`
		Resources *[]string `json:"resources"`
	}
	if err := json.Unmarshal(raw, &decision); err != nil {
		return "", nil, fmt.Errorf("unmarshal policy decision: %w", err)
	}
	if decision.Resources == nil {
		if decision.Resource == "" {
			return decision.Action, nil, nil
		}
		return decision.Action, []string{quoteGlob(decision.Resource, literalGlobChars)}, nil
	}
	if decision.Resource != "" {
		return "", nil, errors.New("policy decision names both resource and resources")
	}
	resources = make([]string, len(*decision.Resources))
	for i, r := range *decision.Resources {
		resources[i] = quoteGlob(r, wildcardQuotedChars)
	}
	return decision.Action, resources, nil
}

const (
	// literalGlobChars is every character path.Match reads as syntax on the
	// pattern side ('*', '?', '[' and the escape itself); ']' outside a class is
	// already literal once '[' is quoted. The same set internal/ai/permission
	// quotes a concrete name with, which keeps a single-resource grant key
	// byte-for-byte what it was before resources became globs.
	literalGlobChars = `*?[\`
	// wildcardQuotedChars is what a PolicyResources resource quotes: everything
	// but its two wildcards.
	wildcardQuotedChars = `[]\`
)

func quoteGlob(s, chars string) string {
	if !strings.ContainsAny(s, chars) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 4)
	for i := range len(s) {
		if strings.IndexByte(chars, s[i]) >= 0 {
			b.WriteByte('\\')
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
