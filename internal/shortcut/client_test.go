package shortcut

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type recordingSleeper struct {
	mu    sync.Mutex
	calls []time.Duration
}

func (r *recordingSleeper) sleep(ctx context.Context, d time.Duration) error {
	r.mu.Lock()
	r.calls = append(r.calls, d)
	r.mu.Unlock()
	return nil
}

func (r *recordingSleeper) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

func newTestClient(t *testing.T, h http.Handler) (*Client, *recordingSleeper) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	sl := &recordingSleeper{}
	c, err := New(Options{
		BaseURL:    srv.URL + "/api/v3",
		Token:      "test-token",
		HTTPClient: srv.Client(),
		Sleep:      sl.sleep,
		Timeout:    5 * time.Second,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c, sl
}

func TestNewValidation(t *testing.T) {
	t.Parallel()
	if _, err := New(Options{BaseURL: "https://x/api", Token: ""}); err == nil {
		t.Error("expected error for empty token")
	}
	if _, err := New(Options{BaseURL: "not a url", Token: "t"}); err == nil {
		t.Error("expected error for bad base URL")
	}
	if _, err := New(Options{BaseURL: "/relative", Token: "t"}); err == nil {
		t.Error("expected error for relative base URL")
	}
}

func TestRequestHeaders(t *testing.T) {
	t.Parallel()
	var gotToken, gotAccept, gotUA, gotCT, gotRawQuery string
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotToken = r.Header.Get("Shortcut-Token")
		gotAccept = r.Header.Get("Accept")
		gotUA = r.Header.Get("User-Agent")
		gotCT = r.Header.Get("Content-Type")
		gotRawQuery = r.URL.RawQuery
		fmt.Fprint(w, `{"id":"uuid","mention_name":"matheus","name":"Matheus"}`)
	}))
	if _, err := c.CurrentMember(context.Background()); err != nil {
		t.Fatalf("CurrentMember: %v", err)
	}
	if gotToken != "test-token" {
		t.Errorf("Shortcut-Token = %q", gotToken)
	}
	if gotAccept != "application/json" || gotCT != "application/json" {
		t.Errorf("content headers wrong: accept=%q ct=%q", gotAccept, gotCT)
	}
	if !strings.Contains(gotUA, "herdr-shortcut/") {
		t.Errorf("User-Agent = %q", gotUA)
	}
	if strings.Contains(gotRawQuery, "test-token") {
		t.Errorf("token leaked into query string: %q", gotRawQuery)
	}
}

func TestCurrentMember(t *testing.T) {
	t.Parallel()
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v3/member" {
			t.Errorf("path = %q", r.URL.Path)
		}
		fmt.Fprint(w, `{"id":"abc-uuid","mention_name":"matheus","name":"Matheus O"}`)
	}))
	m, err := c.CurrentMember(context.Background())
	if err != nil {
		t.Fatalf("CurrentMember: %v", err)
	}
	if m.ID != "abc-uuid" || m.MentionName != "matheus" || m.Name != "Matheus O" {
		t.Errorf("member = %+v", m)
	}
}

func TestWorkflows(t *testing.T) {
	t.Parallel()
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[{"id":1,"name":"Eng","states":[{"id":10,"name":"In Progress","type":"started","position":1}]}]`)
	}))
	ws, err := c.Workflows(context.Background())
	if err != nil {
		t.Fatalf("Workflows: %v", err)
	}
	if len(ws) != 1 || len(ws[0].States) != 1 || ws[0].States[0].Type != "started" {
		t.Errorf("workflows = %+v", ws)
	}
}

func TestStoryNullableDecoding(t *testing.T) {
	t.Parallel()
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v3/stories/42" {
			t.Errorf("path = %q", r.URL.Path)
		}
		fmt.Fprint(w, `{
			"id":42,"name":"Fix bug","description":"details","app_url":"https://app.shortcut.com/o/story/42",
			"story_type":"bug","workflow_state_id":10,
			"estimate":null,"deadline":null,"group_id":null,"epic_id":null,
			"labels":[{"id":1,"name":"backend"},{"id":2,"name":"urgent"}],
			"completed":false,"archived":false
		}`)
	}))
	s, err := c.Story(context.Background(), 42)
	if err != nil {
		t.Fatalf("Story: %v", err)
	}
	if s.Estimate != nil || s.Deadline != nil || s.EpicID != nil {
		t.Errorf("nullable fields should be nil: %+v", s)
	}
	if s.BranchName() != "" || s.Team() != "" {
		t.Errorf("absent branch/team should be empty")
	}
	if got := s.LabelNames(); len(got) != 2 || got[0] != "backend" {
		t.Errorf("labels = %v", got)
	}
}

func TestStoryPopulatedFields(t *testing.T) {
	t.Parallel()
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{
			"id":7,"name":"x","app_url":"https://app.shortcut.com/o/story/7","story_type":"feature",
			"workflow_state_id":5,"estimate":3,"deadline":"2026-08-01T15:04:05Z",
			"formatted_vcs_branch_name":"m/sc-7-x","group_id":"grp-uuid","epic_id":99,
			"updated_at":"2026-07-01T00:00:00Z","labels":[],"completed":false,"archived":false
		}`)
	}))
	s, err := c.Story(context.Background(), 7)
	if err != nil {
		t.Fatalf("Story: %v", err)
	}
	if s.Estimate == nil || *s.Estimate != 3 {
		t.Errorf("estimate = %v", s.Estimate)
	}
	if s.Deadline == nil || s.Deadline.Year() != 2026 {
		t.Errorf("deadline = %v", s.Deadline)
	}
	if s.BranchName() != "m/sc-7-x" || s.Team() != "grp-uuid" {
		t.Errorf("branch/team = %q/%q", s.BranchName(), s.Team())
	}
	if s.EpicID == nil || *s.EpicID != 99 {
		t.Errorf("epic = %v", s.EpicID)
	}
}

func TestMyStoriesQueryRenderingAndPagination(t *testing.T) {
	t.Parallel()
	var firstQuery, firstDetail, firstPageSize string
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		switch q.Get("cursor") {
		case "":
			firstQuery = q.Get("query")
			firstDetail = q.Get("detail")
			firstPageSize = q.Get("page_size")
			fmt.Fprint(w, `{"total":3,"data":[{"id":1},{"id":2}],"next":"/api/v3/search/stories?cursor=p2"}`)
		case "p2":
			// story 2 repeated (dedup) plus a new story 3, no more pages
			fmt.Fprint(w, `{"total":3,"data":[{"id":2},{"id":3}],"next":null}`)
		default:
			t.Errorf("unexpected cursor %q", q.Get("cursor"))
		}
	}))
	stories, err := c.MyStories(context.Background(), "matheus", "owner:{member} is:story !is:done !is:archived", 100, 250)
	if err != nil {
		t.Fatalf("MyStories: %v", err)
	}
	if firstQuery != "owner:matheus is:story !is:done !is:archived" {
		t.Errorf("query = %q", firstQuery)
	}
	if firstDetail != "full" || firstPageSize != "100" {
		t.Errorf("detail=%q page_size=%q", firstDetail, firstPageSize)
	}
	var ids []int64
	for _, s := range stories {
		ids = append(ids, s.ID)
	}
	if fmt.Sprint(ids) != "[1 2 3]" {
		t.Errorf("stories (deduped, ordered) = %v", ids)
	}
}

func TestMyStoriesMaxLimit(t *testing.T) {
	t.Parallel()
	pages := 0
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pages++
		fmt.Fprint(w, `{"total":5,"data":[{"id":1},{"id":2},{"id":3}],"next":"/api/v3/search/stories?cursor=more"}`)
	}))
	stories, err := c.MyStories(context.Background(), "m", "owner:{member}", 100, 2)
	if err != nil {
		t.Fatalf("MyStories: %v", err)
	}
	if len(stories) != 2 {
		t.Errorf("expected 2 stories (max), got %d", len(stories))
	}
	if pages != 1 {
		t.Errorf("should stop after reaching max without extra pages; pages=%d", pages)
	}
}

func TestMyStoriesQuotesUnsafeMention(t *testing.T) {
	t.Parallel()
	var gotQuery string
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("query")
		fmt.Fprint(w, `{"total":0,"data":[],"next":null}`)
	}))
	if _, err := c.MyStories(context.Background(), "first last", "owner:{member}", 50, 50); err != nil {
		t.Fatalf("MyStories: %v", err)
	}
	if gotQuery != `owner:"first last"` {
		t.Errorf("unsafe mention should be quoted, got %q", gotQuery)
	}
}

func TestMyStoriesAllowsSparseAndDuplicatePages(t *testing.T) {
	t.Parallel()
	// pageSize 1; every other page repeats an earlier id, so the unique count
	// trails the raw count and more than ceil(maxStories/pageSize) pages are
	// needed. Distinct cursors keep the loop detector quiet. This must be allowed
	// (up to the 1,000 raw-match ceiling), not mistaken for a runaway.
	nextID := int64(1)
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := 0
		if cur := r.URL.Query().Get("cursor"); cur != "" {
			fmt.Sscanf(cur, "p%d", &page)
		}
		var id int64
		if page%2 == 0 {
			id = nextID
			nextID++
		} else {
			id = 1 // duplicate of the first story
		}
		fmt.Fprintf(w, `{"total":100,"data":[{"id":%d}],"next":"/api/v3/search/stories?cursor=p%d"}`, id, page+1)
	}))
	stories, err := c.MyStories(context.Background(), "m", "owner:{member}", 1, 5)
	if err != nil {
		t.Fatalf("sparse/duplicate pages must be allowed: %v", err)
	}
	if len(stories) != 5 {
		t.Errorf("expected 5 unique stories across sparse/duplicate pages, got %d", len(stories))
	}
}

func TestMyStoriesPaginationLoop(t *testing.T) {
	t.Parallel()
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Always point next at the same cursor to force a loop.
		fmt.Fprint(w, `{"total":9,"data":[{"id":1}],"next":"/api/v3/search/stories?cursor=loop"}`)
	}))
	_, err := c.MyStories(context.Background(), "m", "owner:{member}", 100, 250)
	if err == nil || !strings.Contains(err.Error(), "pagination loop") {
		t.Fatalf("expected pagination loop error, got %v", err)
	}
}

func TestMyStoriesRejectsCrossOriginNext(t *testing.T) {
	t.Parallel()
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"total":9,"data":[{"id":1}],"next":"https://evil.example.com/api/v3/search/stories?cursor=x"}`)
	}))
	_, err := c.MyStories(context.Background(), "m", "owner:{member}", 100, 250)
	if err == nil || !strings.Contains(err.Error(), "different origin") {
		t.Fatalf("expected different-origin error, got %v", err)
	}
}

func TestRetryOn429HonorsRetryAfter(t *testing.T) {
	t.Parallel()
	var calls int
	c, sl := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Retry-After", "2")
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprint(w, `{"message":"rate limited"}`)
			return
		}
		fmt.Fprint(w, `{"id":"u","mention_name":"m","name":"n"}`)
	}))
	if _, err := c.CurrentMember(context.Background()); err != nil {
		t.Fatalf("expected success after retry: %v", err)
	}
	if calls != 2 {
		t.Errorf("expected 2 calls, got %d", calls)
	}
	if sl.count() != 1 || sl.calls[0] != 2*time.Second {
		t.Errorf("expected one 2s Retry-After sleep, got %v", sl.calls)
	}
}

func TestParseRetryAfterCapsHugeSeconds(t *testing.T) {
	t.Parallel()
	c, err := New(Options{BaseURL: "https://api.app.shortcut.com/api/v3", Token: "t"})
	if err != nil {
		t.Fatal(err)
	}
	// A hostile, overflow-inducing value is capped before the duration conversion,
	// never wrapping to a negative or tiny delay.
	if d := c.parseRetryAfter("99999999999"); d <= 0 || d > maxRetryDelay {
		t.Errorf("huge Retry-After should be capped to (0, %v], got %v", maxRetryDelay, d)
	}
	if d := c.parseRetryAfter("2"); d != 2*time.Second {
		t.Errorf("normal Retry-After should pass through: %v", d)
	}
}

func TestDNSFailureIsNotRetried(t *testing.T) {
	t.Parallel()
	// A DNS failure is not on the transient allowlist, so it must not be retried.
	sl := &recordingSleeper{}
	c, err := New(Options{BaseURL: "http://does-not-exist.invalid/api/v3", Token: "t", Timeout: 2 * time.Second, Sleep: sl.sleep})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.CurrentMember(context.Background()); err == nil {
		t.Fatal("expected a network error")
	}
	if sl.count() != 0 {
		t.Errorf("a DNS failure must not be retried; got %d sleeps", sl.count())
	}
}

func TestRetryOn5xxThenExhaust(t *testing.T) {
	t.Parallel()
	var calls int
	c, sl := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprint(w, `{"message":"down"}`)
	}))
	_, err := c.CurrentMember(context.Background())
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 503 {
		t.Fatalf("expected 503 APIError, got %v", err)
	}
	if calls != 4 { // 1 initial + 3 retries
		t.Errorf("expected 4 calls, got %d", calls)
	}
	if sl.count() != 3 {
		t.Errorf("expected 3 backoff sleeps, got %d", sl.count())
	}
}

func TestNoRetryOnClientError(t *testing.T) {
	t.Parallel()
	tests := []struct {
		status int
		check  func(*APIError) bool
	}{
		{401, (*APIError).IsAuth},
		{403, (*APIError).IsAuth},
		{404, (*APIError).IsNotFound},
		{422, func(e *APIError) bool { return e.StatusCode == 422 }},
	}
	for _, tc := range tests {
		t.Run(fmt.Sprint(tc.status), func(t *testing.T) {
			t.Parallel()
			var calls int
			c, sl := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.WriteHeader(tc.status)
				fmt.Fprintf(w, `{"message":"error %d"}`, tc.status)
			}))
			_, err := c.CurrentMember(context.Background())
			var apiErr *APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("expected APIError, got %v", err)
			}
			if !tc.check(apiErr) {
				t.Errorf("predicate failed for %d: %+v", tc.status, apiErr)
			}
			if calls != 1 || sl.count() != 0 {
				t.Errorf("client error must not retry: calls=%d sleeps=%d", calls, sl.count())
			}
		})
	}
}

func TestMalformedJSON(t *testing.T) {
	t.Parallel()
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{not valid json`)
	}))
	_, err := c.CurrentMember(context.Background())
	if err == nil || !strings.Contains(err.Error(), "malformed JSON") {
		t.Fatalf("expected malformed JSON error, got %v", err)
	}
}

func TestNetworkErrorRetriedThenReturned(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close() // now the server refuses connections
	sl := &recordingSleeper{}
	c, err := New(Options{BaseURL: url + "/api/v3", Token: "t", Sleep: sl.sleep})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.CurrentMember(context.Background())
	var netErr *NetworkError
	if !errors.As(err, &netErr) {
		t.Fatalf("expected NetworkError, got %v", err)
	}
	if sl.count() != 3 {
		t.Errorf("expected 3 retry sleeps for network error, got %d", sl.count())
	}
}

func TestContextCancellation(t *testing.T) {
	t.Parallel()
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{}`)
	}))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.CurrentMember(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestRequestTimeout(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release // block until the test lets go
	}))
	t.Cleanup(func() { close(release); srv.Close() })
	sl := &recordingSleeper{}
	c, err := New(Options{BaseURL: srv.URL + "/api/v3", Token: "t", HTTPClient: srv.Client(), Timeout: 20 * time.Millisecond, Sleep: sl.sleep})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.CurrentMember(context.Background())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline exceeded, got %v", err)
	}
}
