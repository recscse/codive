// Package ui provides a production-grade terminal UI for codive.
// Design language: clean monochrome structure with selective accent color,
// aligned columns, no decorative emojis — inspired by Claude Code & Cargo.
package ui

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/fatih/color"
)

// Terminal style primitives
var (
	// Accent — used sparingly for primary value, success, or brand identity
	accent    = color.New(color.FgHiGreen)
	accentB   = color.New(color.FgHiGreen, color.Bold)

	// Muted — secondary text, labels, dividers
	muted     = color.New(color.Faint)
	mutedB    = color.New(color.Faint, color.Bold)

	// Primary — command/section titles, symbol names
	primary   = color.New(color.Bold)
	secondary = color.New(color.FgWhite)

	// Semantic
	errorC   = color.New(color.FgRed, color.Bold)
	warnC    = color.New(color.FgYellow)
	infoC    = color.New(color.FgCyan)
	kindC    = color.New(color.FgHiBlue)

	// Exported aliases used by other packages
	Dim      = muted
	Bold     = primary
	Green    = accent
	GreenBold = accentB
	Cyan     = infoC
	CyanBold = color.New(color.FgCyan, color.Bold)
	Yellow   = warnC
	Red      = errorC
	Magenta  = color.New(color.FgMagenta)
	MagentaBold = color.New(color.FgMagenta, color.Bold)
	WhiteBold   = color.New(color.FgHiWhite, color.Bold)
)

// SetNoColor enables or disables ANSI color output.
func SetNoColor(noColor bool) {
	color.NoColor = noColor
}

// Count formats a quantity with correct singular/plural noun agreement,
// e.g. Count(1, "file", "files") -> "1 file", Count(3, "file", "files") -> "3 files".
func Count(n int, singular, plural string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", singular)
	}
	return fmt.Sprintf("%d %s", n, plural)
}

// PrintJSON marshals data with indentation and writes to stdout.
func PrintJSON(data any) error {
	bytes, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode json: %w", err)
	}
	fmt.Println(string(bytes))
	return nil
}

// ─── Layout primitives ────────────────────────────────────────────────────────

// SectionHeader prints a top-level section title.
// Visually: a blank line, then "  codive · <title>" with a rule beneath.
//
//	  codive  Search Results
//	  ──────────────────────
func SectionHeader(title string) {
	fmt.Println()
	fmt.Printf("  %s  %s\n", muted.Sprint("codive"), primary.Sprint(title))
	fmt.Printf("  %s\n", muted.Sprint(strings.Repeat("─", 56)))
}

// SubHeader prints a secondary indented section title.
func SubHeader(title string) {
	fmt.Println()
	fmt.Printf("  %s\n", primary.Sprint(title))
}

// Divider prints a full-width muted rule.
func Divider() {
	fmt.Printf("  %s\n", muted.Sprint(strings.Repeat("─", 56)))
}

// BlankLine prints a newline.
func BlankLine() { fmt.Println() }

// ─── Status indicators ────────────────────────────────────────────────────────

// Success prints a check-mark success line.
func Success(msg string) {
	fmt.Printf("  %s  %s\n", accentB.Sprint("✓"), secondary.Sprint(msg))
}

// Info prints an informational line.
func Info(msg string) {
	fmt.Printf("  %s  %s\n", infoC.Sprint("i"), msg)
}

// Warning prints a warning line.
func Warning(msg string) {
	fmt.Fprintf(os.Stderr, "  %s  %s\n", warnC.Sprint("!"), msg)
}

// Error prints an error line to stderr.
func Error(msg string) {
	fmt.Fprintf(os.Stderr, "\n  %s  %s\n\n", errorC.Sprint("error:"), msg)
}

// CheckPass prints a passing diagnostic check.
func CheckPass(msg string) {
	fmt.Printf("  %s  %s\n", accentB.Sprint("✓"), msg)
}

// CheckWarn prints a warning diagnostic check.
func CheckWarn(msg string) {
	fmt.Printf("  %s  %s\n", warnC.Sprint("–"), msg)
}

// CheckFail prints a failing diagnostic check.
func CheckFail(msg string) {
	fmt.Printf("  %s  %s\n", errorC.Sprint("✗"), msg)
}

// ─── Data display ─────────────────────────────────────────────────────────────

// KeyValue prints a left-aligned label with a right-side value.
// Both columns are aligned to a fixed label width.
func KeyValue(key, value string) {
	fmt.Printf("  %-20s  %s\n", muted.Sprint(key), value)
}

// KeyValueAccent prints a key-value row where the value is highlighted.
func KeyValueAccent(key, value string) {
	fmt.Printf("  %-20s  %s\n", muted.Sprint(key), accentB.Sprint(value))
}

// KeyValueHighlight is an alias for KeyValueAccent (backward-compatible).
func KeyValueHighlight(key, value string) { KeyValueAccent(key, value) }

// Label renders a standalone dimmed label (for group headings in output).
func Label(text string) {
	fmt.Printf("  %s\n", mutedB.Sprint(text))
}

// ListItem renders an indented bullet row with an optional dim annotation.
func ListItem(text, annotation string) {
	if annotation != "" {
		fmt.Printf("    %s  %-36s  %s\n", muted.Sprint("·"), text, muted.Sprint(annotation))
	} else {
		fmt.Printf("    %s  %s\n", muted.Sprint("·"), text)
	}
}

// NumberedItem renders a numbered list entry for search/symbol results.
func NumberedItem(n int, primary_, secondary_ string) {
	fmt.Printf("  %s  %s\n", muted.Sprintf("%3d.", n), accentB.Sprint(primary_))
	if secondary_ != "" {
		fmt.Printf("       %s\n", muted.Sprint(secondary_))
	}
}

// CodeLine renders an indented source code snippet line with a gutter bar.
func CodeLine(line string) {
	fmt.Printf("       %s  %s\n", muted.Sprint("│"), line)
}

// Kind renders a symbol kind badge — short, bracketed, colored.
func Kind(k string) string {
	return kindC.Sprintf("[%s]", k)
}

// ─── Progress bar ─────────────────────────────────────────────────────────────

// ProgressBar renders a minimal, real-time terminal progress bar.
// Design: "  Scanning    [████░░░░░░]  47%  (1,234/2,600 files · 8,200/s)"
type ProgressBar struct {
	Total          int
	Current        int
	Title          string
	Unit           string
	BarLength      int
	StartTime      time.Time
	LastRenderTime time.Time
}

// NewProgressBar initializes a new ProgressBar.
func NewProgressBar(total int, title string, unit string) *ProgressBar {
	if total <= 0 {
		total = 1
	}
	return &ProgressBar{
		Total:     total,
		Title:     title,
		Unit:      unit,
		BarLength: 22,
		StartTime: time.Now(),
	}
}

// Update increments the progress by advance and re-renders (throttled to 40 ms).
func (p *ProgressBar) Update(advance int, _ string) {
	p.Current += advance
	if p.Current > p.Total {
		p.Current = p.Total
	}
	now := time.Now()
	if now.Sub(p.LastRenderTime) < 40*time.Millisecond && p.Current < p.Total {
		return
	}
	p.LastRenderTime = now
	p.render()
}

// SetCurrent sets the current count directly and re-renders.
func (p *ProgressBar) SetCurrent(current int, _ string) {
	p.Current = current
	if p.Current > p.Total {
		p.Current = p.Total
	}
	p.render()
}

func (p *ProgressBar) render() {
	elapsed := time.Since(p.StartTime).Seconds()
	if elapsed <= 0 {
		elapsed = 0.001
	}
	frac := float64(p.Current) / float64(p.Total)
	pct := int(frac * 100)
	filled := int(frac * float64(p.BarLength))
	if filled > p.BarLength {
		filled = p.BarLength
	}

	bar := accent.Sprint(strings.Repeat("█", filled)) +
		muted.Sprint(strings.Repeat("░", p.BarLength-filled))

	rate := float64(p.Current) / elapsed

	// Right side: count and throughput
	right := muted.Sprintf("(%s/%s %s · %s/s)",
		commaSep(p.Current),
		commaSep(p.Total),
		p.Unit,
		commaSep(int(rate)),
	)

	line := fmt.Sprintf("\r  %-14s  [%s]  %3d%%  %s",
		primary.Sprint(p.Title),
		bar,
		pct,
		right,
	)
	// Pad to clear previous output then clip
	padded := line + strings.Repeat(" ", max(0, 100-len(stripANSI(line))))
	fmt.Print(padded)
}

// Finish renders the completed bar and moves to the next line.
func (p *ProgressBar) Finish(message string) {
	p.Current = p.Total
	elapsed := time.Since(p.StartTime).Seconds()
	if elapsed <= 0 {
		elapsed = 0.001
	}
	bar := accent.Sprint(strings.Repeat("█", p.BarLength))
	rate := float64(p.Total) / elapsed

	right := muted.Sprintf("(%s %s in %.2fs · %s/s)",
		commaSep(p.Total),
		p.Unit,
		elapsed,
		commaSep(int(rate)),
	)

	fmt.Printf("\r  %-14s  [%s]  100%%  %s  %s\n",
		primary.Sprint(p.Title),
		bar,
		right,
		accentB.Sprint(message),
	)
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

// commaSep formats an integer with comma thousands separators.
func commaSep(n int) string {
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var out []byte
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, byte(c))
	}
	return string(out)
}

// stripANSI removes ANSI escape codes for length calculation.
func stripANSI(s string) string {
	var out []byte
	inEsc := false
	for i := 0; i < len(s); i++ {
		if s[i] == '\x1b' {
			inEsc = true
			continue
		}
		if inEsc {
			if s[i] == 'm' {
				inEsc = false
			}
			continue
		}
		out = append(out, s[i])
	}
	return string(out)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// Header is an alias for SectionHeader, kept for backward compatibility.
func Header(title string) { SectionHeader(title) }
