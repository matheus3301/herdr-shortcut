package tmpl

import (
	"reflect"
	"testing"
)

func TestParseAndRender(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		raw     string
		allowed []string
		values  map[string]string
		want    string
	}{
		{
			name:    "simple",
			raw:     "sc-{id}",
			allowed: []string{"id"},
			values:  map[string]string{"id": "42"},
			want:    "sc-42",
		},
		{
			name:    "multiple placeholders",
			raw:     "owner:{member} is:story",
			allowed: []string{"member"},
			values:  map[string]string{"member": "matheus"},
			want:    "owner:matheus is:story",
		},
		{
			name:    "missing value renders empty",
			raw:     "a{missing}b",
			allowed: []string{"missing"},
			values:  map[string]string{},
			want:    "ab",
		},
		{
			name:    "escaped braces",
			raw:     "{{id}} = {id}",
			allowed: []string{"id"},
			values:  map[string]string{"id": "7"},
			want:    "{id} = 7",
		},
		{
			name:    "lone closing brace is literal",
			raw:     "value} done",
			allowed: nil,
			values:  nil,
			want:    "value} done",
		},
		{
			name:    "multiline with underscores",
			raw:     "type={story_type}\nbranch={branch_name}",
			allowed: []string{"story_type", "branch_name"},
			values:  map[string]string{"story_type": "feature", "branch_name": "feat/x"},
			want:    "type=feature\nbranch=feat/x",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tpl, err := Parse(tc.raw, tc.allowed)
			if err != nil {
				t.Fatalf("Parse(%q) error: %v", tc.raw, err)
			}
			if got := tpl.Render(tc.values); got != tc.want {
				t.Fatalf("Render() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestParseErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		raw     string
		allowed []string
	}{
		{"unclosed", "hello {id", []string{"id"}},
		{"unknown placeholder", "{secret}", []string{"id"}},
		{"invalid key with space", "{a b}", []string{"a b"}},
		{"invalid key leading digit", "{1id}", []string{"1id"}},
		{"empty key", "{}", []string{""}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := Parse(tc.raw, tc.allowed); err == nil {
				t.Fatalf("Parse(%q) expected error, got nil", tc.raw)
			}
		})
	}
}

func TestKeys(t *testing.T) {
	t.Parallel()
	tpl, err := Parse("{b}{a}{a}{c}", []string{"a", "b", "c"})
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	got := tpl.Keys()
	want := []string{"a", "b", "c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Keys() = %v, want %v", got, want)
	}
	if tpl.Raw() != "{b}{a}{a}{c}" {
		t.Fatalf("Raw() = %q", tpl.Raw())
	}
}
