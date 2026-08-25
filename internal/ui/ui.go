package ui

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/fatih/color"
)

var (
	// Output formatting colors
	CyanBold  = color.New(color.FgCyan, color.Bold)
	Green     = color.New(color.FgGreen)
	GreenBold = color.New(color.FgGreen, color.Bold)
	Yellow    = color.New(color.FgYellow)
	Red       = color.New(color.FgRed, color.Bold)
	Magenta   = color.New(color.FgMagenta)
	Dim       = color.New(color.Faint)
	Bold      = color.New(color.Bold)
)

// SetNoColor manually enables or disables colorized terminal output.
func SetNoColor(noColor bool) {
	color.NoColor = noColor
}

// PrintJSON marshals data with indentation and prints to stdout.
func PrintJSON(data any) error {
	bytes, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode json: %w", err)
	}
	fmt.Println(string(bytes))
	return nil
}

// Header prints a section title in cyan bold.
func Header(title string) {
	CyanBold.Println(title)
}

// Divider prints a subtle line divider.
func Divider() {
	Dim.Println(string(make([]rune, 52)))
}

// Error prints an error message in red to stderr.
func Error(msg string) {
	Red.Fprintf(os.Stderr, "Error: %s\n", msg)
}

// Warning prints a warning message in yellow to stderr.
func Warning(msg string) {
	Yellow.Fprintf(os.Stderr, "Warning: %s\n", msg)
}

// Success prints a success message in green.
func Success(msg string) {
	GreenBold.Println(msg)
}
