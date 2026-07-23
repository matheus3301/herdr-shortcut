// Command herdr-shortcut is a Herdr plugin that shows the authenticated
// Shortcut member's active Stories and launches the selected coding-agent
// harness on the chosen Story.
package main

import (
	"os"

	"github.com/matheus3301/herdr-shortcut/internal/app"
)

func main() {
	os.Exit(app.Main(app.Environment{
		Getenv: os.Getenv,
		Args:   os.Args[1:],
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	}))
}
