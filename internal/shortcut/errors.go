package shortcut

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"unicode"
)

// APIError is a typed, human-readable error for a non-2xx Shortcut response.
type APIError struct {
	StatusCode int
	Endpoint   string
	Message    string
}

func (e *APIError) Error() string { return e.Message }

// IsAuth reports an authentication or authorization failure (401/403).
func (e *APIError) IsAuth() bool { return e.StatusCode == 401 || e.StatusCode == 403 }

// IsRateLimit reports a 429 rate-limit response.
func (e *APIError) IsRateLimit() bool { return e.StatusCode == 429 }

// IsNotFound reports a 404 response.
func (e *APIError) IsNotFound() bool { return e.StatusCode == 404 }

// NetworkError wraps a transport-level failure without exposing the full URL.
type NetworkError struct {
	Endpoint string
	cause    error
}

func (e *NetworkError) Error() string {
	return fmt.Sprintf("%s: network error contacting Shortcut: %v", e.Endpoint, e.cause)
}

func (e *NetworkError) Unwrap() error { return e.cause }

// newNetworkError builds a NetworkError, unwrapping *url.Error so the full URL
// (never a secret, but noisy) is not repeated in the message.
func newNetworkError(label string, err error) *NetworkError {
	cause := err
	var uerr *url.Error
	if errors.As(err, &uerr) {
		cause = uerr.Err
	}
	return &NetworkError{Endpoint: label, cause: cause}
}

func messageForStatus(code int, label, snippet string) string {
	var base string
	switch code {
	case 401:
		base = "Shortcut authentication failed (401): verify your API token"
	case 403:
		base = "Shortcut access forbidden (403): the token lacks permission for this request"
	case 404:
		base = "Shortcut resource not found (404)"
	case 422:
		base = "Shortcut rejected the request (422): check the search query or parameters"
	case 429:
		base = "Shortcut rate limit exceeded (429): too many requests, retry shortly"
	default:
		if code >= 500 {
			base = fmt.Sprintf("Shortcut server error (%d)", code)
		} else {
			base = fmt.Sprintf("Shortcut request failed (%d)", code)
		}
	}
	msg := label + ": " + base
	if snippet != "" {
		msg += " — " + snippet
	}
	return msg
}

// sanitizeSnippet strips control characters and collapses whitespace in an API
// error body so it is safe to embed in an error message, bounded by the caller's
// read limit.
func sanitizeSnippet(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	var b strings.Builder
	b.Grow(len(body))
	lastSpace := false
	for _, r := range string(body) {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			if !lastSpace {
				b.WriteByte(' ')
				lastSpace = true
			}
			continue
		}
		b.WriteRune(r)
		lastSpace = false
	}
	return strings.TrimSpace(b.String())
}
