// Package ui provides a clean, modern, and rich terminal user interface inspired by modern developer tools.
package ui

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

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

// ProgressBar renders a clean, real-time terminal progress bar with items/s and ETA.
type ProgressBar struct {
	Total          int
	Current        int
	Title          string
	Unit           string
	BarLength      int
	StartTime      time.Time
	LastRenderTime time.Time
}

// NewProgressBar initializes a new terminal progress bar.
func NewProgressBar(total int, title string, unit string) *ProgressBar {
	if total <= 0 {
		total = 1
	}
	return &ProgressBar{
		Total:     total,
		Title:     title,
		Unit:      unit,
		BarLength: 28,
		StartTime: time.Now(),
	}
}

// Update increments the progress bar count and refreshes terminal display.
func (p *ProgressBar) Update(advance int, currentItem string) {
	p.Current += advance
	if p.Current > p.Total {
		p.Current = p.Total
	}
	now := time.Now()
	if now.Sub(p.LastRenderTime) < 30*time.Millisecond && p.Current < p.Total {
		return
	}
	p.LastRenderTime = now
	p.render(currentItem)
}

// SetCurrent sets the current progress count directly.
func (p *ProgressBar) SetCurrent(current int, currentItem string) {
	p.Current = current
	if p.Current > p.Total {
		p.Current = p.Total
	}
	p.render(currentItem)
}

func (p *ProgressBar) render(currentItem string) {
	elapsed := time.Since(p.StartTime).Seconds()
	if elapsed <= 0 {
		elapsed = 0.001
	}
	frac := float64(p.Current) / float64(p.Total)
	percent := int(frac * 100)
	filled := int(frac * float64(p.BarLength))
	if filled > p.BarLength {
		filled = p.BarLength
	}

	bar := strings.Repeat("█", filled) + strings.Repeat("░", p.BarLength-filled)
	rate := float64(p.Current) / elapsed
	
	dispItem := currentItem
	if len(dispItem) > 30 {
		dispItem = "..." + dispItem[len(dispItem)-27:]
	}

	etaStr := ""
	if rate > 0 && p.Current < p.Total {
		eta := float64(p.Total-p.Current) / rate
		etaStr = fmt.Sprintf(" | ETA: %.1fs", eta)
	}

	line := fmt.Sprintf("\r%s [%s] %3d%% (%d/%d %s | %.0f %s/s%s) %s",
		WhiteBold.Sprint(p.Title),
		Green.Sprint(bar),
		percent,
		p.Current,
		p.Total,
		p.Unit,
		rate,
		p.Unit,
		etaStr,
		Dim.Sprint(dispItem),
	)
	if len(line) < 100 {
		line += strings.Repeat(" ", 100-len(line))
	}
	fmt.Print(line[:100])
}

// Finish marks the progress bar as 100% complete and moves to a new line.
func (p *ProgressBar) Finish(message string) {
	p.Current = p.Total
	elapsed := time.Since(p.StartTime).Seconds()
	if elapsed <= 0 {
		elapsed = 0.001
	}
	bar := strings.Repeat("█", p.BarLength)
	rate := float64(p.Total) / elapsed

	fmt.Printf("\r%s [%s] 100%% (%d/%d %s in %.2fs | %.0f %s/s) - %s\n",
		WhiteBold.Sprint(p.Title),
		Green.Sprint(bar),
		p.Total,
		p.Total,
		p.Unit,
		elapsed,
		rate,
		p.Unit,
		GreenBold.Sprint(message),
	)
}
