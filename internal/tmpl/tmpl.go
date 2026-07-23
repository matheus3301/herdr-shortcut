// Package tmpl implements a tiny, deterministic placeholder template engine
// used across herdr-shortcut for the search query, agent name, tab label, and
// launch prompt.
//
// The grammar is intentionally minimal so that templates can be validated at
// configuration time and rendered without a shell or Go's text/template:
//
//	{key}   substitutes the value of key (missing keys render as the empty
//	        string); key must match [A-Za-z_][A-Za-z0-9_]*.
//	{{      renders a literal '{'.
//	}}      renders a literal '}'.
//
// A lone '{' that does not begin a valid placeholder is a parse error, which
// surfaces to the user as a configuration error before anything is launched.
package tmpl

import (
	"fmt"
	"sort"
	"strings"
)

// Template is a parsed placeholder template. It is safe for concurrent reads.
type Template struct {
	raw   string
	parts []part
	keys  map[string]struct{}
}

type part struct {
	text        string // literal text when placeholder is false
	key         string // placeholder key when placeholder is true
	placeholder bool
}

// Parse parses raw and verifies that every placeholder it uses is present in
// allowed. It returns an actionable error otherwise so callers can treat an
// invalid template as a configuration error.
func Parse(raw string, allowed []string) (*Template, error) {
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, a := range allowed {
		allowedSet[a] = struct{}{}
	}
	t := &Template{raw: raw, keys: make(map[string]struct{})}
	var lit strings.Builder
	flush := func() {
		if lit.Len() > 0 {
			t.parts = append(t.parts, part{text: lit.String()})
			lit.Reset()
		}
	}
	for i := 0; i < len(raw); {
		c := raw[i]
		switch c {
		case '{':
			if i+1 < len(raw) && raw[i+1] == '{' {
				lit.WriteByte('{')
				i += 2
				continue
			}
			rel := strings.IndexByte(raw[i+1:], '}')
			if rel < 0 {
				return nil, fmt.Errorf("unclosed placeholder starting at position %d", i)
			}
			key := raw[i+1 : i+1+rel]
			if !validKey(key) {
				return nil, fmt.Errorf("invalid placeholder %q", "{"+key+"}")
			}
			if _, ok := allowedSet[key]; !ok {
				return nil, fmt.Errorf("unknown placeholder %q (allowed: %s)", "{"+key+"}", strings.Join(sortedSet(allowedSet), ", "))
			}
			flush()
			t.parts = append(t.parts, part{key: key, placeholder: true})
			t.keys[key] = struct{}{}
			i += 1 + rel + 1
		case '}':
			if i+1 < len(raw) && raw[i+1] == '}' {
				lit.WriteByte('}')
				i += 2
				continue
			}
			lit.WriteByte('}')
			i++
		default:
			lit.WriteByte(c)
			i++
		}
	}
	flush()
	return t, nil
}

// Render substitutes placeholders using values. Placeholders absent from values
// render as the empty string; the engine never emits Go formatting artifacts.
func (t *Template) Render(values map[string]string) string {
	var b strings.Builder
	for _, p := range t.parts {
		if p.placeholder {
			b.WriteString(values[p.key])
		} else {
			b.WriteString(p.text)
		}
	}
	return b.String()
}

// Keys returns the sorted set of placeholder keys used by the template.
func (t *Template) Keys() []string {
	return sortedSet(t.keys)
}

// Raw returns the original template text.
func (t *Template) Raw() string { return t.raw }

func validKey(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r == '_':
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case i > 0 && r >= '0' && r <= '9':
		default:
			return false
		}
	}
	return true
}

func sortedSet(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
