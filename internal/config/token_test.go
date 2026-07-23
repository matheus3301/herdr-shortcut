package config

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

// TestMain lets this test binary re-exec itself as a fake token command so the
// real (shell-free) default runner is exercised without external programs.
func TestMain(m *testing.M) {
	if len(os.Args) >= 2 && os.Args[1] == "__token_helper__" {
		mode := ""
		if len(os.Args) >= 3 {
			mode = os.Args[2]
		}
		runTokenHelper(mode)
		return
	}
	os.Exit(m.Run())
}

func runTokenHelper(mode string) {
	switch mode {
	case "ok":
		_, _ = os.Stdout.WriteString("secret-token-value\n")
	case "crlf":
		_, _ = os.Stdout.WriteString("secret-token-value\r\n")
	case "nonewline":
		_, _ = os.Stdout.WriteString("secret-token-value")
	case "empty":
		// no output
	case "fail":
		_, _ = os.Stdout.WriteString("LEAKED_STDOUT_TOKEN")
		_, _ = os.Stderr.WriteString("LEAKED_STDERR_TOKEN")
		os.Exit(7)
	case "envcheck":
		if v, ok := os.LookupEnv("SHORTCUT_API_TOKEN"); ok {
			_, _ = os.Stdout.WriteString(v)
		}
		os.Exit(0)
	case "flood":
		big := make([]byte, 512*1024)
		for i := range big {
			big[i] = 'a'
		}
		_, _ = os.Stdout.Write(big)
		os.Exit(0)
	}
	os.Exit(0)
}

func helperCommand(mode string) []string {
	return []string{os.Args[0], "__token_helper__", mode}
}

func TestResolveTokenEnvWins(t *testing.T) {
	t.Parallel()
	called := false
	r := TokenResolver{
		Getenv: func(k string) string {
			if k == TokenEnvVar {
				return "env-token"
			}
			return ""
		},
		Runner: func(ctx context.Context, argv []string) ([]byte, error) {
			called = true
			return []byte("cmd-token"), nil
		},
	}
	tok, src, err := r.ResolveToken(context.Background(), []string{"anything"})
	if err != nil {
		t.Fatalf("ResolveToken: %v", err)
	}
	if tok != "env-token" || src != TokenSourceEnv {
		t.Fatalf("got %q/%q", tok, src)
	}
	if called {
		t.Fatal("token command should not run when env var is set")
	}
}

func TestResolveTokenCommandInjected(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		out  string
		want string
	}{
		{"trailing lf", "tok\n", "tok"},
		{"trailing crlf", "tok\r\n", "tok"},
		{"trailing cr", "tok\r", "tok"},
		{"no newline", "tok", "tok"},
		{"internal spaces preserved", "a b\n", "a b"},
		{"only one newline trimmed", "tok\n\n", "tok\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := TokenResolver{
				Getenv: func(string) string { return "" },
				Runner: func(ctx context.Context, argv []string) ([]byte, error) {
					return []byte(tc.out), nil
				},
			}
			tok, src, err := r.ResolveToken(context.Background(), []string{"cmd"})
			if err != nil {
				t.Fatalf("ResolveToken: %v", err)
			}
			if tok != tc.want || src != TokenSourceCommand {
				t.Fatalf("got %q/%q, want %q", tok, src, tc.want)
			}
		})
	}
}

func TestResolveTokenEmptyOutput(t *testing.T) {
	t.Parallel()
	r := TokenResolver{
		Getenv: func(string) string { return "" },
		Runner: func(ctx context.Context, argv []string) ([]byte, error) { return []byte("\n"), nil },
	}
	if _, _, err := r.ResolveToken(context.Background(), []string{"cmd"}); err == nil {
		t.Fatal("expected error for empty token output")
	}
}

func TestResolveTokenNone(t *testing.T) {
	t.Parallel()
	r := TokenResolver{Getenv: func(string) string { return "" }}
	_, src, err := r.ResolveToken(context.Background(), nil)
	if !errors.Is(err, ErrNoToken) {
		t.Fatalf("expected ErrNoToken, got %v", err)
	}
	if src != TokenSourceNone {
		t.Fatalf("src = %q", src)
	}
}

func TestDefaultRunnerRealExec(t *testing.T) {
	t.Parallel()
	r := TokenResolver{Getenv: func(string) string { return "" }}
	tok, src, err := r.ResolveToken(context.Background(), helperCommand("ok"))
	if err != nil {
		t.Fatalf("ResolveToken: %v", err)
	}
	if tok != "secret-token-value" || src != TokenSourceCommand {
		t.Fatalf("got %q/%q", tok, src)
	}
}

func TestDefaultRunnerScrubsTokenFromChild(t *testing.T) {
	// Not parallel: mutates the process environment.
	t.Setenv("SHORTCUT_API_TOKEN", "env-secret")
	out, err := runArgv(context.Background(), helperCommand("envcheck"))
	if err != nil {
		t.Fatalf("runArgv: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("token command child inherited the token: %q", out)
	}
}

func TestDefaultRunnerFailsOnOversizedOutput(t *testing.T) {
	t.Parallel()
	// A token command that floods stdout past the cap must fail closed rather than
	// return a truncated prefix (which would silently become a wrong token).
	out, err := runArgv(context.Background(), helperCommand("flood"))
	if err == nil {
		t.Fatalf("expected an overflow error, got %d bytes", len(out))
	}
	if !strings.Contains(err.Error(), "exceeded") {
		t.Errorf("expected an overflow error, got %v", err)
	}
}

func TestResolveTokenFailsOnOversizedCommandOutput(t *testing.T) {
	t.Parallel()
	r := TokenResolver{Getenv: func(string) string { return "" }}
	_, _, err := r.ResolveToken(context.Background(), helperCommand("flood"))
	if err == nil {
		t.Fatal("oversized token command output must fail, not truncate")
	}
}

func TestDefaultRunnerRedactsOutputOnFailure(t *testing.T) {
	t.Parallel()
	r := TokenResolver{Getenv: func(string) string { return "" }}
	_, _, err := r.ResolveToken(context.Background(), helperCommand("fail"))
	if err == nil {
		t.Fatal("expected error from failing command")
	}
	msg := err.Error()
	if strings.Contains(msg, "LEAKED_STDOUT_TOKEN") || strings.Contains(msg, "LEAKED_STDERR_TOKEN") {
		t.Fatalf("error leaked command output: %q", msg)
	}
}
