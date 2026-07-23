// Package herdr is a typed adapter around the Herdr v0.7.5 CLI. Every command
// is invoked via argv (never a shell) using the injected HERDR_BIN_PATH, and
// Herdr's JSON response envelopes are parsed into typed results. Herdr error
// codes and messages are preserved without dumping unbounded output.
package herdr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/matheus3301/herdr-shortcut/internal/childenv"
)

// maxOutputBytes bounds how much stdout/stderr the adapter retains per command.
const maxOutputBytes = 1 << 20 // 1 MiB

// CommandResult is the outcome of running a Herdr CLI command.
type CommandResult struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

// Runner executes the Herdr binary with args. A non-zero process exit is
// reported via CommandResult.ExitCode, not err; err is reserved for failures to
// start the process or context cancellation.
type Runner func(ctx context.Context, bin string, args []string) (CommandResult, error)

// Herdr is a typed Herdr CLI adapter.
type Herdr struct {
	bin    string
	runner Runner
}

// New builds an adapter. bin should be the injected HERDR_BIN_PATH; it falls
// back to "herdr" on PATH for standalone development. A nil runner uses the real
// process runner.
func New(bin string, runner Runner) *Herdr {
	if strings.TrimSpace(bin) == "" {
		bin = "herdr"
	}
	if runner == nil {
		runner = DefaultRunner
	}
	return &Herdr{bin: bin, runner: runner}
}

// Bin returns the resolved Herdr binary path.
func (h *Herdr) Bin() string { return h.bin }

// envelope is the common Herdr JSON response shape. There is no top-level "ok":
// success carries result, failure carries error.
type envelope struct {
	ID     string          `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *errorPayload   `json:"error"`
}

type errorPayload struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// HerdrError is a typed error preserving Herdr's error code/message or a bounded
// snippet of non-JSON output.
type HerdrError struct {
	Command  string
	Code     string
	Message  string
	ExitCode int
}

func (e *HerdrError) Error() string {
	switch {
	case e.Code != "":
		return fmt.Sprintf("herdr %s failed (%s): %s", e.Command, e.Code, e.Message)
	case e.Message != "":
		return fmt.Sprintf("herdr %s failed (exit %d): %s", e.Command, e.ExitCode, e.Message)
	default:
		return fmt.Sprintf("herdr %s failed (exit %d)", e.Command, e.ExitCode)
	}
}

// run executes a command and returns the raw result payload on success. It
// preserves Herdr error envelopes and bounds any output included in errors.
func (h *Herdr) run(ctx context.Context, args []string) (json.RawMessage, error) {
	label := commandLabel(args)
	res, err := h.runner(ctx, h.bin, args)
	if err != nil {
		return nil, fmt.Errorf("herdr %s: %w", label, err)
	}
	if res.ExitCode != 0 {
		return nil, parseError(label, res.ExitCode, res.Stderr)
	}
	var env envelope
	if err := json.Unmarshal(res.Stdout, &env); err != nil {
		// Exit 0 but unparseable: the command may have taken effect server-side.
		return nil, &HerdrError{Command: label, Code: malformedResponseCode, Message: "unexpected non-JSON output: " + snippet(res.Stdout)}
	}
	if env.Error != nil {
		return nil, &HerdrError{Command: label, Code: env.Error.Code, Message: snippet([]byte(env.Error.Message))}
	}
	if len(env.Result) == 0 {
		return nil, &HerdrError{Command: label, Code: malformedResponseCode, Message: "response contained no result"}
	}
	return env.Result, nil
}

// runTyped runs a command, validates the result.type discriminator, and decodes
// the result payload into out (when non-nil).
func (h *Herdr) runTyped(ctx context.Context, args []string, wantType string, out any) error {
	raw, err := h.run(ctx, args)
	if err != nil {
		return err
	}
	if got := resultType(raw); got != wantType {
		return &HerdrError{Command: commandLabel(args), Code: unexpectedResultTypeCode, Message: fmt.Sprintf("unexpected result type %q (want %q)", got, wantType)}
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return &HerdrError{Command: commandLabel(args), Code: malformedResponseCode, Message: "could not parse " + wantType + " result"}
		}
	}
	return nil
}

// resultType extracts the snake_case result.type discriminator.
func resultType(raw json.RawMessage) string {
	var t struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal(raw, &t)
	return t.Type
}

// parseError converts a failed command into a typed error, preferring Herdr's
// structured error envelope (exit 1) over plain-text usage output (exit 2). The
// message is always sanitized and bounded.
func parseError(label string, exitCode int, stderr []byte) error {
	var env envelope
	if err := json.Unmarshal(stderr, &env); err == nil && env.Error != nil {
		return &HerdrError{Command: label, Code: env.Error.Code, Message: snippet([]byte(env.Error.Message)), ExitCode: exitCode}
	}
	return &HerdrError{Command: label, Message: snippet(stderr), ExitCode: exitCode}
}

// commandLabel builds a short label from the leading non-flag arguments, e.g.
// "tab create" or "agent start".
func commandLabel(args []string) string {
	var parts []string
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			break
		}
		parts = append(parts, a)
		if len(parts) == 3 {
			break
		}
	}
	if len(parts) == 0 {
		return "command"
	}
	return strings.Join(parts, " ")
}

// snippet returns a bounded, single-line, control-character-free view of output
// for inclusion in an error message.
func snippet(b []byte) string {
	s := string(b)
	if len(s) > 512 {
		s = s[:512] + "…"
	}
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || r == '\r' {
			return ' '
		}
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
	return strings.TrimSpace(strings.Join(strings.Fields(s), " "))
}

// DefaultRunner runs the real Herdr binary, capturing bounded stdout/stderr.
// The child never inherits the Shortcut token from the environment.
func DefaultRunner(ctx context.Context, bin string, args []string) (CommandResult, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = childenv.Sanitized(os.Environ())
	var stdout, stderr cappedBuffer
	stdout.max = maxOutputBytes
	stderr.max = maxOutputBytes
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	res := CommandResult{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}
	if ctx.Err() != nil {
		return res, ctx.Err()
	}
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			res.ExitCode = ee.ExitCode()
			return res, nil
		}
		return res, err
	}
	return res, nil
}

// cappedBuffer captures up to max bytes and silently drops the rest so a
// runaway subprocess cannot exhaust memory. It reports full writes so the child
// process does not receive a write error.
type cappedBuffer struct {
	buf bytes.Buffer
	max int
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	if remaining := c.max - c.buf.Len(); remaining > 0 {
		if remaining < len(p) {
			c.buf.Write(p[:remaining])
		} else {
			c.buf.Write(p)
		}
	}
	return len(p), nil
}

func (c *cappedBuffer) Bytes() []byte { return c.buf.Bytes() }
