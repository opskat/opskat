package policy

import (
	"errors"
	"path"
	"strings"
	"unicode/utf8"
)

// An extension resource is a path.Match glob (pkg/extension decodePolicyDecision):
// a literal resource arrives with every glob character quoted and stands for
// exactly the name it quotes; a PolicyResources resource keeps '*' / '?' as
// wildcards and stands for every name it could match (an index pattern such as
// logs-*). A rule's resource glob is judged against that set of names:
//
//   - a deny hits when the two globs may match a common name (globMayMatch);
//   - an allow or grant covers the resource only when its glob matches every
//     name the resource stands for (globCovers).
//
// For a resource without wildcards both reduce to path.Match(glob, name) — the
// matching every single-resource extension has always had. Where a wildcard
// resource's answer cannot be worked out (a malformed glob, a character class the
// comparison does not model), the deny side counts it as a hit and the allow side
// as not covered.

// globMayMatch reports whether glob may match one of the names resource stands for.
func globMayMatch(glob, resource string) bool {
	res, err := parseGlob(resource)
	if err != nil {
		return true
	}
	if name, literal := res.literal(); literal {
		return matchGlobPattern(glob, name)
	}
	g, err := parseGlob(glob)
	if err != nil {
		return true
	}
	return globsOverlap(g, res)
}

// globCovers reports whether glob matches every name resource stands for.
func globCovers(glob, resource string) bool {
	res, err := parseGlob(resource)
	if err != nil {
		return false
	}
	if name, literal := res.literal(); literal {
		return matchGlobPattern(glob, name)
	}
	g, err := parseGlob(glob)
	if err != nil {
		return false
	}
	return globCoversTokens(g, res)
}

// DisplayExtensionResource renders a resource glob as the extension returned it:
// the quoting the host added is dropped, so a literal resource shows as its name
// and a wildcard resource as the pattern the extension wrote.
func DisplayExtensionResource(resource string) string {
	if !strings.Contains(resource, `\`) {
		return resource
	}
	var b strings.Builder
	b.Grow(len(resource))
	for i := 0; i < len(resource); i++ {
		if resource[i] == '\\' && i+1 < len(resource) {
			i++
		}
		b.WriteByte(resource[i])
	}
	return b.String()
}

type globKind uint8

const (
	globLit   globKind = iota // one literal character
	globAny                   // '?': one character other than '/'
	globStar                  // '*': any run of characters other than '/'
	globClass                 // '[...]': one character from a class
)

type globTok struct {
	kind  globKind
	lit   rune
	class string // the class as written, brackets included (globClass)
}

type globToks []globTok

var errBadGlob = errors.New("malformed glob")

// parseGlob tokenizes a path.Match pattern, refusing what path.Match would.
func parseGlob(p string) (globToks, error) {
	var toks globToks
	for i := 0; i < len(p); {
		switch p[i] {
		case '*':
			if n := len(toks); n == 0 || toks[n-1].kind != globStar {
				toks = append(toks, globTok{kind: globStar})
			}
			i++
		case '?':
			toks = append(toks, globTok{kind: globAny})
			i++
		case '[':
			end, err := classEnd(p, i)
			if err != nil {
				return nil, err
			}
			toks = append(toks, globTok{kind: globClass, class: p[i:end]})
			i = end
		case '\\':
			if i+1 >= len(p) {
				return nil, errBadGlob
			}
			r, size := utf8.DecodeRuneInString(p[i+1:])
			toks = append(toks, globTok{kind: globLit, lit: r})
			i += 1 + size
		default:
			r, size := utf8.DecodeRuneInString(p[i:])
			toks = append(toks, globTok{kind: globLit, lit: r})
			i += size
		}
	}
	return toks, nil
}

// classEnd returns the index just past the class starting at p[start] == '['.
func classEnd(p string, start int) (int, error) {
	j := start + 1
	if j < len(p) && p[j] == '^' {
		j++
	}
	first := j
	for j < len(p) {
		switch {
		case p[j] == ']' && j > first:
			class := p[start : j+1]
			if _, err := path.Match(class, "a"); err != nil {
				return 0, errBadGlob
			}
			return j + 1, nil
		case p[j] == '\\':
			j += 2
		default:
			j++
		}
	}
	return 0, errBadGlob
}

// literal returns the name a wildcard-free glob stands for.
func (t globToks) literal() (string, bool) {
	var b strings.Builder
	for _, tok := range t {
		if tok.kind != globLit {
			return "", false
		}
		b.WriteRune(tok.lit)
	}
	return b.String(), true
}

// globsOverlap decides whether some name matches both globs: a reachability
// search over position pairs, each step consuming one character both sides can
// produce (a '*' on either side may also consume nothing).
func globsOverlap(a, b globToks) bool {
	seen := make(map[[2]int]bool)
	var reach func(i, j int) bool
	reach = func(i, j int) bool {
		if i == len(a) && j == len(b) {
			return true
		}
		if seen[[2]int{i, j}] {
			return false
		}
		seen[[2]int{i, j}] = true
		if i < len(a) && a[i].kind == globStar {
			if reach(i+1, j) || (j < len(b) && b[j].kind != globStar && mayBeUnslashed(b[j]) && reach(i, j+1)) {
				return true
			}
		}
		if j < len(b) && b[j].kind == globStar {
			if reach(i, j+1) || (i < len(a) && a[i].kind != globStar && mayBeUnslashed(a[i]) && reach(i+1, j)) {
				return true
			}
		}
		return i < len(a) && j < len(b) && a[i].kind != globStar && b[j].kind != globStar &&
			charsMayMeet(a[i], b[j]) && reach(i+1, j+1)
	}
	return reach(0, 0)
}

// mayBeUnslashed: the single-character token can produce a character a '*' matches.
// A class is assumed to (the deny side's conservative answer).
func mayBeUnslashed(t globTok) bool {
	return t.kind != globLit || t.lit != '/'
}

// charsMayMeet: two single-character tokens can produce the same character.
// A class against anything but a literal is assumed to (conservative for deny).
func charsMayMeet(x, y globTok) bool {
	if x.kind == globLit && y.kind == globLit {
		return x.lit == y.lit
	}
	if y.kind == globLit {
		x, y = y, x
	}
	if x.kind == globLit {
		switch y.kind {
		case globAny:
			return x.lit != '/'
		case globClass:
			return matchGlobPattern(y.class, string(x.lit))
		}
	}
	return true
}

// globCoversTokens decides, soundly but not completely, whether every name res
// stands for matches g: g's tokens must consume res's tokens so that each g token
// is guaranteed to match whatever the res tokens it consumes produce. When no such
// consumption exists the answer is "not covered" — which is also the answer for
// the rare inclusions this cannot see (a class, or '?*' against '*?').
func globCoversTokens(g, res globToks) bool {
	seen := make(map[[2]int]bool)
	var reach func(i, j int) bool
	reach = func(i, j int) bool {
		if i == len(g) {
			return j == len(res)
		}
		if seen[[2]int{i, j}] {
			return false
		}
		seen[[2]int{i, j}] = true
		if j < len(res) && tokenCovers(g[i], res[j]) && reach(i+1, j+1) {
			return true
		}
		if g[i].kind != globStar {
			return false
		}
		// g's '*' consumes nothing more, or also the next res token when every
		// character that token produces is one '*' matches.
		return reach(i+1, j) || (j < len(res) && starAbsorbs(res[j]) && reach(i, j+1))
	}
	return reach(0, 0)
}

// tokenCovers: g token matches every string the single res token produces.
func tokenCovers(g, r globTok) bool {
	switch g.kind {
	case globStar:
		return starAbsorbs(r)
	case globAny:
		return r.kind == globAny || (r.kind == globLit && r.lit != '/')
	case globLit:
		return r.kind == globLit && r.lit == g.lit
	default: // globClass
		return r.kind == globLit && matchGlobPattern(g.class, string(r.lit))
	}
}

func starAbsorbs(r globTok) bool {
	switch r.kind {
	case globStar, globAny:
		return true
	case globLit:
		return r.lit != '/'
	default:
		return false
	}
}
