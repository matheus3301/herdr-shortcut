package config

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/matheus3301/herdr-shortcut/internal/childenv"
)

// maxTokenOutputBytes bounds how much stdout a token command may produce, so a
// runaway command cannot exhaust memory.
const maxTokenOutputBytes = 1 << 16 // 64 KiB

// TokenEnvVar is the environment variable checked first for the Shortcut token.
const TokenEnvVar = "SHORTCUT_API_TOKEN"

// ErrNoToken is returned when neither the environment variable nor a token
// command is configured.
var ErrNoToken = errors.New("no Shortcut API token: set SHORTCUT_API_TOKEN or configure shortcut.token_command")

// TokenSource names where a token came from, for diagnostics. It never contains
// the token itself.
type TokenSource string

const (
	TokenSourceEnv     TokenSource = "SHORTCUT_API_TOKEN environment variable"
	TokenSourceCommand TokenSource = "shortcut.token_command"
	TokenSourceNone    TokenSource = "none"
)

// CommandRunner runs an argv command with no shell and returns its stdout. It
// must not include the command's output in any returned error.
type CommandRunner func(ctx context.Context, argv []string) ([]byte, error)

// TokenResolver resolves the Shortcut API token with injectable dependencies so
// tests never touch the real environment or spawn real processes.
type TokenResolver struct {
	Getenv func(string) string
	Runner CommandRunner
}

// ResolveToken applies the documented precedence:
//
//  1. a non-empty SHORTCUT_API_TOKEN environment variable, then
//  2. shortcut.token_command executed directly (never via a shell), trimming
//     one trailing newline from stdout.
//
// The returned token is a transient value; callers must not persist it. Errors
// never contain the token or the command's output.
func (r TokenResolver) ResolveToken(ctx context.Context, tokenCommand []string) (string, TokenSource, error) {
	getenv := r.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	if tok := getenv(TokenEnvVar); tok != "" {
		return tok, TokenSourceEnv, nil
	}
	if len(tokenCommand) > 0 {
		runner := r.Runner
		if runner == nil {
			runner = runArgv
		}
		out, err := runner(ctx, tokenCommand)
		if err != nil {
			// Only reference the program name and the error, never output.
			return "", TokenSourceNone, fmt.Errorf("token command %q failed: %w", tokenCommand[0], err)
		}
		tok := trimOneTrailingNewline(out)
		if tok == "" {
			return "", TokenSourceNone, fmt.Errorf("token command %q produced no token output", tokenCommand[0])
		}
		return tok, TokenSourceCommand, nil
	}
	return "", TokenSourceNone, ErrNoToken
}

// runArgv is the default CommandRunner. It executes argv directly, captures only
// stdout, and discards stderr so a failing command cannot leak secrets through
// an error message.
func runArgv(ctx context.Context, argv []string) ([]byte, error) {
	if len(argv) == 0 {
		return nil, errors.New("empty command")
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Env = childenv.Sanitized(os.Environ())
	var stdout limitedBuffer
	stdout.max = maxTokenOutputBytes
	cmd.Stdout = &stdout
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	if stdout.overflow {
		// Fail closed: a truncated prefix would silently become a wrong token.
		return nil, fmt.Errorf("token command output exceeded %d bytes", maxTokenOutputBytes)
	}
	return stdout.Bytes(), nil
}

// limitedBuffer captures up to max bytes and records whether more was written,
// reporting full writes so the child process is not disrupted.
type limitedBuffer struct {
	buf      bytes.Buffer
	max      int
	overflow bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	remaining := b.max - b.buf.Len()
	if remaining >= len(p) {
		b.buf.Write(p)
	} else {
		if remaining > 0 {
			b.buf.Write(p[:remaining])
		}
		b.overflow = true
	}
	return len(p), nil
}

func (b *limitedBuffer) Bytes() []byte { return b.buf.Bytes() }

// trimOneTrailingNewline removes a single trailing line terminator ("\n",
// "\r\n", or "\r") without touching other whitespace that may belong to the
// token.
func trimOneTrailingNewline(b []byte) string {
	s := string(b)
	switch {
	case strings.HasSuffix(s, "\r\n"):
		return s[:len(s)-2]
	case strings.HasSuffix(s, "\n"), strings.HasSuffix(s, "\r"):
		return s[:len(s)-1]
	default:
		return s
	}
}
