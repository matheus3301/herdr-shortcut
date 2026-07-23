package tui

import (
	"context"
	"errors"

	"github.com/matheus3301/herdr-shortcut/internal/shortcut"
)

// asCredentialsError returns the CredentialsError in err's chain, or nil.
func asCredentialsError(err error) *CredentialsError {
	var c CredentialsError
	if errors.As(err, &c) {
		return &c
	}
	return nil
}

// classifyError returns a short human label for a load error to headline the
// error state, or "" for a generic error.
func classifyError(err error) string {
	if err == nil {
		return ""
	}
	var apiErr *shortcut.APIError
	if errors.As(err, &apiErr) {
		switch {
		case apiErr.IsAuth():
			return "Authentication failed"
		case apiErr.IsRateLimit():
			return "Rate limited by Shortcut"
		case apiErr.IsNotFound():
			return "Not found"
		default:
			return "Shortcut API error"
		}
	}
	var netErr *shortcut.NetworkError
	if errors.As(err, &netErr) {
		return "Network error"
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "Request timed out"
	}
	return ""
}
