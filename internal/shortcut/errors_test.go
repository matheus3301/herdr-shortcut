package shortcut

import (
	"errors"
	"testing"
	"time"
)

func TestAPIErrorPredicates(t *testing.T) {
	t.Parallel()
	cases := []struct {
		code                 int
		auth, rate, notFound bool
	}{
		{401, true, false, false},
		{403, true, false, false},
		{404, false, false, true},
		{429, false, true, false},
		{500, false, false, false},
	}
	for _, c := range cases {
		e := &APIError{StatusCode: c.code, Message: "x"}
		if e.IsAuth() != c.auth || e.IsRateLimit() != c.rate || e.IsNotFound() != c.notFound {
			t.Errorf("code %d predicates wrong: auth=%v rate=%v nf=%v", c.code, e.IsAuth(), e.IsRateLimit(), e.IsNotFound())
		}
		if e.Error() != "x" {
			t.Errorf("Error() = %q", e.Error())
		}
	}
}

func TestNetworkErrorUnwrap(t *testing.T) {
	t.Parallel()
	base := errors.New("connection refused")
	ne := &NetworkError{Endpoint: "GET /member", cause: base}
	if !errors.Is(ne, base) {
		t.Error("NetworkError should unwrap to its cause")
	}
	if ne.Error() == "" {
		t.Error("NetworkError.Error should be non-empty")
	}
}

func TestCompareUpdatedDesc(t *testing.T) {
	t.Parallel()
	a := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	b := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if compareUpdatedDesc(&a, &b) != -1 { // a newer -> first
		t.Error("newer should sort first")
	}
	if compareUpdatedDesc(&b, &a) != 1 {
		t.Error("older should sort later")
	}
	if compareUpdatedDesc(&a, &a) != 0 {
		t.Error("equal should be 0")
	}
	if compareUpdatedDesc(nil, &a) != 1 || compareUpdatedDesc(&a, nil) != -1 || compareUpdatedDesc(nil, nil) != 0 {
		t.Error("nil handling wrong")
	}
}
