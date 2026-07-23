package app

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/matheus3301/herdr-shortcut/internal/childenv"
)

// defaultClipboard copies text to the system clipboard without a shell, trying
// the platform's clipboard tools in order. The child never inherits the Shortcut
// token. It is used for the copyable, credential-free recovery command.
func defaultClipboard(text string) error {
	candidates := clipboardCommands(runtime.GOOS)
	if len(candidates) == 0 {
		return errors.New("clipboard copy is not supported on this platform")
	}
	var lastErr error
	for _, c := range candidates {
		if _, err := exec.LookPath(c.name); err != nil {
			lastErr = err
			continue
		}
		cmd := exec.Command(c.name, c.args...)
		cmd.Env = childenv.Sanitized(os.Environ())
		cmd.Stdin = strings.NewReader(text)
		if err := cmd.Run(); err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	if lastErr == nil {
		lastErr = errors.New("no clipboard tool found")
	}
	return fmt.Errorf("could not copy to clipboard: %w", lastErr)
}

type clipboardCmd struct {
	name string
	args []string
}

func clipboardCommands(goos string) []clipboardCmd {
	switch goos {
	case "darwin":
		return []clipboardCmd{{name: "pbcopy"}}
	case "linux":
		return []clipboardCmd{
			{name: "wl-copy"},
			{name: "xclip", args: []string{"-selection", "clipboard"}},
			{name: "xsel", args: []string{"--clipboard", "--input"}},
		}
	default:
		return nil
	}
}
