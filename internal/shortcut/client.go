// Package shortcut is a typed, read-only client for the Shortcut REST API v3.
// It authenticates with the Shortcut-Token header, honors context cancellation
// and a configured timeout, bounds all body reads, and never places the token
// in a URL, log, or error.
package shortcut

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"

	"github.com/matheus3301/herdr-shortcut/internal/buildinfo"
	"github.com/matheus3301/herdr-shortcut/internal/tmpl"
)

const (
	defaultMaxRetries = 3
	retryBaseDelay    = 250 * time.Millisecond
	maxRetryDelay     = 30 * time.Second
	// maxResponseBytes bounds a decoded response body.
	maxResponseBytes = 32 << 20 // 32 MiB
	// maxErrorBodyBytes bounds the sanitized snippet included in an error.
	maxErrorBodyBytes = 2 << 10 // 2 KiB
	// hardStoryCap is the absolute ceiling on returned Stories regardless of
	// configuration, matching Shortcut's 1,000-match search limit. Pagination also
	// counts RAW returned entries against this ceiling.
	hardStoryCap = 1000
	// maxEmptyPageStreak bounds how many consecutive empty pages (each with a
	// distinct next token) are tolerated before giving up, so an API that never
	// completes cannot loop forever without repeating a token.
	maxEmptyPageStreak = 5
	// maxRedirects bounds same-origin redirects before failing.
	maxRedirects = 5
)

// SleepFunc waits for d or until ctx is done. It is injectable so retry backoff
// is deterministic in tests.
type SleepFunc func(ctx context.Context, d time.Duration) error

// Options configure a Client.
type Options struct {
	BaseURL    string
	Token      string
	HTTPClient *http.Client
	UserAgent  string
	Timeout    time.Duration
	// Sleep overrides the retry backoff sleeper; defaults to a real timer.
	Sleep SleepFunc
	// MaxRetries overrides the retry count for idempotent GETs (0-3); defaults 3.
	MaxRetries *int
	// Now overrides the clock used for HTTP-date Retry-After; defaults time.Now.
	Now func() time.Time
}

// Client is a Shortcut REST API v3 client.
type Client struct {
	baseURL    *url.URL
	token      string
	httpClient *http.Client
	userAgent  string
	timeout    time.Duration
	sleep      SleepFunc
	maxRetries int
	now        func() time.Time
}

// errCrossOriginRedirect marks a redirect that would leave the base origin.
var errCrossOriginRedirect = errors.New("cross-origin redirect blocked")

// New builds a Client. The token is required and is held only in memory. A
// same-origin redirect policy is always installed (even over an injected
// client, via a shallow copy) so the Shortcut-Token header can never follow a
// redirect to a different origin.
func New(opts Options) (*Client, error) {
	if strings.TrimSpace(opts.Token) == "" {
		return nil, errors.New("shortcut: API token is required")
	}
	base, err := url.Parse(opts.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("shortcut: invalid base URL: %w", err)
	}
	if base.Scheme == "" || base.Host == "" {
		return nil, fmt.Errorf("shortcut: base URL must be absolute, got %q", opts.BaseURL)
	}
	maxRetries := defaultMaxRetries
	if opts.MaxRetries != nil {
		maxRetries = *opts.MaxRetries
		if maxRetries < 0 || maxRetries > defaultMaxRetries {
			return nil, fmt.Errorf("shortcut: MaxRetries must be between 0 and %d, got %d", defaultMaxRetries, maxRetries)
		}
	}

	// Shallow-copy any injected client so we can enforce the same-origin redirect
	// policy without mutating the caller's client.
	var hc http.Client
	if opts.HTTPClient != nil {
		hc = *opts.HTTPClient
	}
	baseScheme, baseHost := base.Scheme, base.Host
	hc.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != baseScheme || req.URL.Host != baseHost {
			return fmt.Errorf("%w: %s://%s", errCrossOriginRedirect, req.URL.Scheme, req.URL.Host)
		}
		if len(via) >= maxRedirects {
			return fmt.Errorf("stopped after %d redirects", maxRedirects)
		}
		return nil
	}

	ua := opts.UserAgent
	if ua == "" {
		ua = buildinfo.UserAgent
	}
	sleep := opts.Sleep
	if sleep == nil {
		sleep = realSleep
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &Client{
		baseURL:    base,
		token:      opts.Token,
		httpClient: &hc,
		userAgent:  ua,
		timeout:    opts.Timeout,
		sleep:      sleep,
		maxRetries: maxRetries,
		now:        now,
	}, nil
}

// CurrentMember returns the authenticated member (GET /member).
func (c *Client) CurrentMember(ctx context.Context) (Member, error) {
	var m Member
	if err := c.get(ctx, c.endpoint("/member", nil), &m); err != nil {
		return Member{}, err
	}
	return m, nil
}

// Workflows returns all workflows with their states (GET /workflows).
func (c *Client) Workflows(ctx context.Context) ([]Workflow, error) {
	var w []Workflow
	if err := c.get(ctx, c.endpoint("/workflows", nil), &w); err != nil {
		return nil, err
	}
	return w, nil
}

// Story returns the complete current Story (GET /stories/{id}).
func (c *Client) Story(ctx context.Context, id int64) (Story, error) {
	var s Story
	if err := c.get(ctx, c.endpoint("/stories/"+strconv.FormatInt(id, 10), nil), &s); err != nil {
		return Story{}, err
	}
	return s, nil
}

// MyStories fetches the member's Stories using queryTemplate (with {member}
// replaced by mention), paging until the next token is empty or maxStories is
// reached. Results are deduplicated by ID with stable order and never exceed
// 1,000 Stories.
func (c *Client) MyStories(ctx context.Context, mention, queryTemplate string, pageSize, maxStories int) ([]Story, error) {
	t, err := tmpl.Parse(queryTemplate, []string{"member"})
	if err != nil {
		return nil, fmt.Errorf("invalid query template: %w", err)
	}
	if containsControl(mention) {
		return nil, fmt.Errorf("invalid member name: contains control characters")
	}
	query := t.Render(map[string]string{"member": quoteSearchValue(mention)})
	if maxStories > hardStoryCap {
		maxStories = hardStoryCap
	}
	if pageSize < 1 {
		pageSize = 1
	}

	q := url.Values{}
	q.Set("query", query)
	q.Set("page_size", strconv.Itoa(pageSize))
	q.Set("detail", "full")
	next := c.endpoint("/search/stories", q)

	var stories []Story
	seenID := make(map[int64]struct{})
	visited := make(map[string]struct{})
	// The loop is bounded by counting RAW returned entries against Shortcut's
	// 1,000 raw-match ceiling — never derived from page_size — so arbitrarily
	// sparse or duplicate-heavy pages are allowed to page through the full match
	// set. Two independent safeguards keep it bounded regardless of the API:
	// repeated `next` tokens are rejected, and a run of empty pages is capped.
	rawCount := 0
	emptyStreak := 0
	for next != "" && rawCount < hardStoryCap {
		if _, loop := visited[next]; loop {
			return nil, fmt.Errorf("shortcut pagination loop detected at %s", pathLabel(next))
		}
		visited[next] = struct{}{}

		var res storySearchResults
		if err := c.get(ctx, next, &res); err != nil {
			return nil, err
		}
		rawCount += len(res.Data)
		if len(res.Data) == 0 {
			emptyStreak++
			if emptyStreak > maxEmptyPageStreak {
				return nil, fmt.Errorf("shortcut returned %d consecutive empty pages without completing", emptyStreak)
			}
		} else {
			emptyStreak = 0
		}
		for _, s := range res.Data {
			if _, dup := seenID[s.ID]; dup {
				continue
			}
			seenID[s.ID] = struct{}{}
			stories = append(stories, s)
			if len(stories) >= maxStories {
				return stories, nil
			}
		}
		if res.Next == nil || *res.Next == "" {
			break
		}
		resolved, err := c.resolveNext(*res.Next)
		if err != nil {
			return nil, err
		}
		next = resolved
	}
	return stories, nil
}

// endpoint builds an absolute URL by appending path to the configured base URL
// path (which itself includes the /api/v3 prefix).
func (c *Client) endpoint(path string, query url.Values) string {
	u := *c.baseURL
	u.Path = strings.TrimRight(u.Path, "/") + "/" + strings.TrimLeft(path, "/")
	if query != nil {
		u.RawQuery = query.Encode()
	}
	return u.String()
}

// resolveNext resolves a pagination reference against the base URL and refuses
// to follow it to a different origin, so an API response cannot redirect the
// authenticated request (and its token header) to another host.
func (c *Client) resolveNext(next string) (string, error) {
	ref, err := url.Parse(next)
	if err != nil {
		return "", fmt.Errorf("shortcut returned an invalid pagination URL: %v", err)
	}
	resolved := c.baseURL.ResolveReference(ref)
	if resolved.Scheme != c.baseURL.Scheme || resolved.Host != c.baseURL.Host {
		return "", fmt.Errorf("refusing to follow pagination URL to a different origin: %s://%s", resolved.Scheme, resolved.Host)
	}
	return resolved.String(), nil
}

func (c *Client) get(ctx context.Context, fullURL string, out any) error {
	label := "GET " + pathLabel(fullURL)
	var lastErr error
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		retryable, retryAfter, err := c.attempt(ctx, http.MethodGet, fullURL, label, out)
		if err == nil {
			return nil
		}
		lastErr = err
		if !retryable || attempt == c.maxRetries {
			break
		}
		if serr := c.sleep(ctx, c.backoff(attempt, retryAfter)); serr != nil {
			return serr
		}
	}
	return lastErr
}

// attempt performs a single request. It returns whether the failure is
// retryable and any Retry-After hint. The response body is always closed.
func (c *Client) attempt(ctx context.Context, method, fullURL, label string, out any) (retryable bool, retryAfter time.Duration, err error) {
	reqCtx := ctx
	if c.timeout > 0 {
		var cancel context.CancelFunc
		reqCtx, cancel = context.WithTimeout(ctx, c.timeout)
		defer cancel()
	}
	req, err := http.NewRequestWithContext(reqCtx, method, fullURL, nil)
	if err != nil {
		return false, 0, fmt.Errorf("%s: build request: %w", label, err)
	}
	c.setHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			// The caller cancelled or timed out the whole operation.
			return false, 0, ctx.Err()
		}
		// Per-request deadlines, cross-origin redirects, and other permanent
		// transport failures are not retried; only genuinely transient ones are.
		return isTransientTransport(err), 0, newNetworkError(label, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return false, 0, c.decode(resp, label, out)
	}

	apiErr := c.readAPIError(resp, label)
	if isRetryableStatus(resp.StatusCode) {
		return true, c.parseRetryAfter(resp.Header.Get("Retry-After")), apiErr
	}
	return false, 0, apiErr
}

// isTransientTransport reports whether a transport error is worth retrying using
// a POSITIVE allowlist: only a small set of known-transient connection failures
// (connection refused/reset, broken pipe, and unexpected EOF) are retried.
// Everything else — timeouts, cancellations, blocked cross-origin redirects, DNS
// failures, TLS errors, and any unrecognized error — is treated as permanent so
// the client never retries an error it does not understand.
func isTransientTransport(err error) bool {
	// Cancellations and deadlines are never transient, even if some wrapped errno
	// would otherwise match.
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	for _, se := range []syscall.Errno{syscall.ECONNREFUSED, syscall.ECONNRESET, syscall.EPIPE} {
		if errors.Is(err, se) {
			return true
		}
	}
	return false
}

func (c *Client) setHeaders(req *http.Request) {
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Shortcut-Token", c.token)
}

func (c *Client) decode(resp *http.Response, label string, out any) error {
	if out == nil {
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return newNetworkError(label, err)
	}
	if int64(len(body)) > maxResponseBytes {
		return fmt.Errorf("%s: response exceeded %d bytes", label, maxResponseBytes)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return &APIError{StatusCode: resp.StatusCode, Endpoint: label, Message: label + ": malformed JSON response from Shortcut"}
	}
	return nil
}

func (c *Client) readAPIError(resp *http.Response, label string) *APIError {
	// Never surface the response body for authentication/authorization failures,
	// and always redact the token from any body we do surface.
	var snippet string
	if resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusForbidden {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
		// Redact on the RAW bytes first: whitespace normalization could otherwise
		// split the token across the collapse and let a fragment survive.
		snippet = sanitizeSnippet([]byte(redactToken(string(body), c.token)))
	}
	return &APIError{
		StatusCode: resp.StatusCode,
		Endpoint:   label,
		Message:    messageForStatus(resp.StatusCode, label, snippet),
	}
}

// redactToken replaces any occurrence of the token in s. Defense in depth: the
// API does not echo the token, but a body must never leak it.
func redactToken(s, token string) string {
	if token == "" {
		return s
	}
	return strings.ReplaceAll(s, token, "[redacted]")
}

func (c *Client) backoff(attempt int, retryAfter time.Duration) time.Duration {
	if retryAfter > 0 {
		return capDelay(retryAfter)
	}
	// Exponential backoff: base, 2*base, 4*base, ...
	d := retryBaseDelay << attempt
	if d < retryBaseDelay { // overflow guard
		d = maxRetryDelay
	}
	return capDelay(d)
}

func capDelay(d time.Duration) time.Duration {
	if d > maxRetryDelay {
		return maxRetryDelay
	}
	if d < 0 {
		return 0
	}
	return d
}

func isRetryableStatus(code int) bool {
	switch code {
	case http.StatusTooManyRequests, // 429
		http.StatusInternalServerError, // 500
		http.StatusBadGateway,          // 502
		http.StatusServiceUnavailable,  // 503
		http.StatusGatewayTimeout:      // 504
		return true
	default:
		return false
	}
}

func (c *Client) parseRetryAfter(v string) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs <= 0 {
			return 0
		}
		// Cap the integer seconds BEFORE converting to a Duration: a huge value
		// (e.g. a hostile "99999999999") would otherwise overflow int64 nanoseconds
		// and wrap to a negative or tiny delay.
		if maxSecs := int(maxRetryDelay / time.Second); secs > maxSecs {
			secs = maxSecs
		}
		return time.Duration(secs) * time.Second
	}
	// HTTP-date form: honor the delta from now, bounded to maxRetryDelay.
	if t, err := http.ParseTime(v); err == nil {
		d := t.Sub(c.now())
		if d <= 0 {
			return 0
		}
		return capDelay(d)
	}
	return 0
}

func realSleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// quoteSearchValue safely renders a search operator value. Simple values are
// used verbatim; anything else is wrapped in double quotes with backslashes and
// embedded quotes escaped (backslashes first), matching Shortcut's search
// syntax.
func quoteSearchValue(v string) string {
	if v != "" && isSimpleSearchValue(v) {
		return v
	}
	escaped := strings.ReplaceAll(v, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `"`, `\"`)
	return `"` + escaped + `"`
}

// containsControl reports whether s has any Unicode control character.
func containsControl(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}

func isSimpleSearchValue(v string) bool {
	for _, r := range v {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-', r == '_', r == '.':
		default:
			return false
		}
	}
	return true
}

// pathLabel returns the method-free path of a URL for use in error messages,
// never including the query string (which is irrelevant and keeps errors short).
func pathLabel(fullURL string) string {
	u, err := url.Parse(fullURL)
	if err != nil {
		return "shortcut endpoint"
	}
	return u.Path
}
