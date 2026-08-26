// Package ui provides a clean, modern, and rich terminal user interface inspired by modern developer tools.
package ui

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/fatih/color"
)

var (
	// Terminal Colors & Styles
	Green     = color.New(color.FgGreen)
	GreenBold = color.New(color.FgGreen, color.Bold)
	Cyan      = color.New(color.FgCyan)
	CyanBold  = color.New(color.FgCyan, color.Bold)
	Yellow    = color.New(color.FgYellow)
	Red       = color.New(color.FgRed, color.Bold)
	Magenta   = color.New(color.FgMagenta)
	MagentaBold = color.New(color.FgMagenta, color.Bold)
	Dim       = color.New(color.Faint)
	Bold      = color.New(color.Bold)
	WhiteBold = color.New(color.FgWhite, color.Bold)
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

// Header prints a crisp, modern section title.
func Header(title string) {
	fmt.Println()
	fmt.Printf("%s %s\n", GreenBold.Sprint("●"), WhiteBold.Sprint(title))
}

// SubHeader prints a category or stage header.
func SubHeader(title string) {
	fmt.Println()
	fmt.Printf("%s %s\n", Dim.Sprint("❯"), Bold.Sprint(title))
}

// Success prints a clean success indicator.
func Success(msg string) {
	fmt.Printf("%s %s\n", GreenBold.Sprint("✔"), WhiteBold.Sprint(msg))
}

// Info prints a clean informational notice.
func Info(msg string) {
	fmt.Printf("%s %s\n", CyanBold.Sprint("ℹ"), msg)
}

// Warning prints a clean warning notice.
func Warning(msg string) {
	fmt.Printf("%s %s\n", Yellow.Sprint("!"), msg)
}

// Error prints an error message to stderr.
func Error(msg string) {
	fmt.Fprintf(os.Stderr, "%s %s\n", Red.Sprint("✖"), msg)
}

// KeyValue prints a neatly formatted key-value line.
func KeyValue(key string, value string) {
	fmt.Printf("  %s %s\n", Dim.Sprintf("%-18s", key+":"), value)
}

// KeyValueHighlight prints a key-value line with high-contrast highlighted value.
func KeyValueHighlight(key string, value string) {
	fmt.Printf("  %s %s\n", Dim.Sprintf("%-18s", key+":"), GreenBold.Sprint(value))
}

// Divider prints a subtle, clean hairline divider.
func Divider() {
	Dim.Println(strings.Repeat("─", 60))
}

// Bullet prints a structured list item.
func Bullet(label string, desc string) {
	fmt.Printf("  %s %s %s\n", Dim.Sprint("•"), Bold.Sprint(label), Dim.Sprint(desc))
}
