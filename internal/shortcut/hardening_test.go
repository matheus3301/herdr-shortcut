package shortcut

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func intp(v int) *int { return &v }

func TestNewValidatesMaxRetries(t *testing.T) {
	t.Parallel()
	if _, err := New(Options{BaseURL: "https://x/api", Token: "t", MaxRetries: intp(4)}); err == nil {
		t.Error("MaxRetries=4 should be rejected")
	}
	if _, err := New(Options{BaseURL: "https://x/api", Token: "t", MaxRetries: intp(-1)}); err == nil {
		t.Error("MaxRetries=-1 should be rejected")
	}
	if _, err := New(Options{BaseURL: "https://x/api", Token: "t", MaxRetries: intp(0)}); err != nil {
		t.Errorf("MaxRetries=0 should be allowed: %v", err)
	}
}

func TestMaxRetriesZeroDoesNotRetry(t *testing.T) {
	t.Parallel()
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)
	sl := &recordingSleeper{}
	c, err := New(Options{BaseURL: srv.URL + "/api/v3", Token: "t", HTTPClient: srv.Client(), Sleep: sl.sleep, MaxRetries: intp(0)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.CurrentMember(context.Background()); err == nil {
		t.Fatal("expected error")
	}
	if calls != 1 || sl.count() != 0 {
		t.Errorf("MaxRetries=0 should mean 1 attempt, 0 sleeps; got calls=%d sleeps=%d", calls, sl.count())
	}
}

func TestBlocksCrossOriginRedirect(t *testing.T) {
	t.Parallel()
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// If the token ever reaches here, the guard failed.
		if r.Header.Get("Shortcut-Token") != "" {
			t.Errorf("token leaked to other origin")
		}
		fmt.Fprint(w, `{"id":"evil"}`)
	}))
	t.Cleanup(other.Close)
	base := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/member", http.StatusFound)
	}))
	t.Cleanup(base.Close)

	// Even with an injected custom client, the same-origin policy is enforced.
	c, err := New(Options{BaseURL: base.URL + "/api/v3", Token: "secret", HTTPClient: &http.Client{}, Sleep: (&recordingSleeper{}).sleep})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.CurrentMember(context.Background())
	if err == nil {
		t.Fatal("cross-origin redirect must fail")
	}
	if !strings.Contains(err.Error(), "cross-origin") && !strings.Contains(err.Error(), "network error") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestFollowsSameOriginRedirect(t *testing.T) {
	t.Parallel()
	var tokenSeenOnFinal bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v3/member":
			http.Redirect(w, r, "/api/v3/member2", http.StatusFound)
		case "/api/v3/member2":
			tokenSeenOnFinal = r.Header.Get("Shortcut-Token") == "t"
			fmt.Fprint(w, `{"id":"u","mention_name":"m","name":"n"}`)
		}
	}))
	t.Cleanup(srv.Close)
	c, _ := New(Options{BaseURL: srv.URL + "/api/v3", Token: "t", HTTPClient: srv.Client(), Sleep: (&recordingSleeper{}).sleep})
	m, err := c.CurrentMember(context.Background())
	if err != nil {
		t.Fatalf("same-origin redirect should be followed: %v", err)
	}
	if m.MentionName != "m" || !tokenSeenOnFinal {
		t.Errorf("member=%+v tokenSeenOnFinal=%v", m, tokenSeenOnFinal)
	}
}

func TestErrorSnippetRedactsTokenAndHidesAuthBody(t *testing.T) {
	t.Parallel()
	// 500 body echoes the token -> must be redacted.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "member") {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"message":"boom token=my-secret-token"}`)
			return
		}
	}))
	t.Cleanup(srv.Close)
	sl := &recordingSleeper{}
	c, _ := New(Options{BaseURL: srv.URL + "/api/v3", Token: "my-secret-token", HTTPClient: srv.Client(), Sleep: sl.sleep})
	_, err := c.CurrentMember(context.Background())
	if err == nil || strings.Contains(err.Error(), "my-secret-token") {
		t.Fatalf("token must be redacted from error: %v", err)
	}
	if !strings.Contains(err.Error(), "[redacted]") {
		t.Errorf("expected redaction marker: %v", err)
	}

	// 401 body must not be surfaced at all.
	authSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"message":"secret-diagnostic-body"}`)
	}))
	t.Cleanup(authSrv.Close)
	c2, _ := New(Options{BaseURL: authSrv.URL + "/api/v3", Token: "t", HTTPClient: authSrv.Client(), Sleep: sl.sleep})
	_, err = c2.CurrentMember(context.Background())
	if err == nil || strings.Contains(err.Error(), "secret-diagnostic-body") {
		t.Fatalf("auth error must not surface the body: %v", err)
	}
}

// TestErrorRedactionRunsBeforeWhitespaceNormalization proves the token is
// redacted from the RAW body before whitespace is collapsed: a token containing
// a tab would otherwise survive the collapse as a de-tabbed fragment.
func TestErrorRedactionRunsBeforeWhitespaceNormalization(t *testing.T) {
	t.Parallel()
	const token = "abc\tdef" // contains a tab, which sanitization collapses to a space
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, `{"message":"boom %s trailing"}`, token)
	}))
	t.Cleanup(srv.Close)
	sl := &recordingSleeper{}
	c, _ := New(Options{BaseURL: srv.URL + "/api/v3", Token: token, HTTPClient: srv.Client(), Sleep: sl.sleep})
	_, err := c.CurrentMember(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	// Neither the raw token nor its whitespace-collapsed form may survive.
	if strings.Contains(err.Error(), token) || strings.Contains(err.Error(), "abc def") {
		t.Fatalf("token survived whitespace normalization: %v", err)
	}
	if !strings.Contains(err.Error(), "[redacted]") {
		t.Errorf("expected redaction marker: %v", err)
	}
}

func TestHTTPDateRetryAfter(t *testing.T) {
	t.Parallel()
	fixedNow := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Retry-After", fixedNow.Add(5*time.Second).UTC().Format(http.TimeFormat))
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		fmt.Fprint(w, `{"id":"u","mention_name":"m","name":"n"}`)
	}))
	t.Cleanup(srv.Close)
	sl := &recordingSleeper{}
	c, _ := New(Options{BaseURL: srv.URL + "/api/v3", Token: "t", HTTPClient: srv.Client(), Sleep: sl.sleep, Now: func() time.Time { return fixedNow }})
	if _, err := c.CurrentMember(context.Background()); err != nil {
		t.Fatalf("should succeed after retry: %v", err)
	}
	if sl.count() != 1 || sl.calls[0] != 5*time.Second {
		t.Errorf("expected one 5s HTTP-date Retry-After sleep, got %v", sl.calls)
	}
}

func TestMyStoriesRejectsControlInMention(t *testing.T) {
	t.Parallel()
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	if _, err := c.MyStories(context.Background(), "bad\x1bname", "owner:{member}", 50, 50); err == nil {
		t.Fatal("control char in mention should be rejected")
	}
}

func TestMyStoriesEscapesBackslashAndQuote(t *testing.T) {
	t.Parallel()
	var gotQuery string
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("query")
		fmt.Fprint(w, `{"total":0,"data":[],"next":null}`)
	}))
	if _, err := c.MyStories(context.Background(), `a\b"c`, "owner:{member}", 50, 50); err != nil {
		t.Fatalf("MyStories: %v", err)
	}
	// backslash escaped first, then the quote.
	if gotQuery != `owner:"a\\b\"c"` {
		t.Errorf("query = %q", gotQuery)
	}
}

func TestMyStoriesPageBound(t *testing.T) {
	t.Parallel()
	// Server always returns a fresh next with EMPTY data -> the empty-page
	// safeguard must stop it (distinct cursors defeat repeated-next detection).
	var n int
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		fmt.Fprintf(w, `{"total":9,"data":[],"next":"/api/v3/search/stories?cursor=%d"}`, n)
	}))
	_, err := c.MyStories(context.Background(), "m", "owner:{member}", 100, 250)
	if err == nil || !strings.Contains(err.Error(), "empty pages") {
		t.Fatalf("expected empty-page safeguard error, got %v", err)
	}
	if n > maxEmptyPageStreak+1 {
		t.Errorf("empty-page safeguard should stop promptly, made %d requests", n)
	}
}

func TestMyStoriesStopsAtRawMatchCeiling(t *testing.T) {
	t.Parallel()
	// Each page returns 250 duplicate entries (unique count stays 1) with a
	// distinct cursor; the loop must stop once the RAW count reaches 1000 (the
	// ceiling), not error, and not be derived from page_size.
	var pages int
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pages++
		var b strings.Builder
		b.WriteString(`{"total":100000,"data":[`)
		for i := 0; i < 250; i++ {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(`{"id":1}`)
		}
		fmt.Fprintf(w, "%s],\"next\":\"/api/v3/search/stories?cursor=p%d\"}", b.String(), pages)
	}))
	stories, err := c.MyStories(context.Background(), "m", "owner:{member}", 250, 1000)
	if err != nil {
		t.Fatalf("hitting the raw ceiling should stop cleanly, not error: %v", err)
	}
	if len(stories) != 1 {
		t.Errorf("expected 1 unique story, got %d", len(stories))
	}
	if pages != 4 { // 4 x 250 raw entries = the 1000 ceiling
		t.Errorf("should stop after 1000 raw matches; made %d page requests", pages)
	}
}

func TestMyStoriesAllowsManySparsePages(t *testing.T) {
	t.Parallel()
	// pageSize 250, maxStories 250; eight sparse pages (well beyond any
	// page_size-derived cap) each with one duplicate, then a final page.
	var pages int
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pages++
		if pages >= 8 {
			fmt.Fprint(w, `{"total":9,"data":[{"id":1}],"next":null}`)
			return
		}
		fmt.Fprintf(w, `{"total":9,"data":[{"id":1}],"next":"/api/v3/search/stories?cursor=p%d"}`, pages)
	}))
	stories, err := c.MyStories(context.Background(), "m", "owner:{member}", 250, 250)
	if err != nil {
		t.Fatalf("many sparse pages must be allowed: %v", err)
	}
	if len(stories) != 1 || pages != 8 {
		t.Errorf("stories=%d pages=%d (want 1 unique across 8 pages)", len(stories), pages)
	}
}
