package cmd

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"

	"github.com/recscse/devctx/internal/ui"
)

// RunLogs reads and prints the most recent log entries from .devctx/devctx.log.
func RunLogs(targetDir string, lineCount int) error {
	if lineCount <= 0 {
		lineCount = 50
	}

	absDir, err := filepath.Abs(targetDir)
	if err != nil {
		return fmt.Errorf("invalid directory path: %w", err)
	}

	logPath := filepath.Join(absDir, ".devctx", "devctx.log")
	file, err := os.Open(logPath)
	if os.IsNotExist(err) {
		ui.Warning(fmt.Sprintf("No log file found at %s. Run ctxd commands first.", logPath))
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to open log file: %w", err)
	}
	defer file.Close()

	var lines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("error reading log file: %w", err)
	}

	if len(lines) == 0 {
		fmt.Println("Log file is empty.")
		return nil
	}

	start := 0
	if len(lines) > lineCount {
		start = len(lines) - lineCount
	}

	ui.Header(fmt.Sprintf("📜 Recent Logs (%s)", logPath))
	fmt.Println()
	for _, line := range lines[start:] {
		fmt.Println(line)
	}

	return nil
}
