package logger

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
)

// LogFileHandle holds the open log file descriptor so it can be closed on shutdown.
var LogFileHandle *os.File

// InitLogger sets up structured logging to .codive/codive.log and optionally to stderr.
func InitLogger(targetDir string, verbose bool) error {
	absDir, err := filepath.Abs(targetDir)
	if err != nil {
		return fmt.Errorf("invalid directory path: %w", err)
	}

	codiveDir := filepath.Join(absDir, ".codive")
	if err := os.MkdirAll(codiveDir, 0755); err != nil {
		return fmt.Errorf("failed to create .codive directory: %w", err)
	}

	logPath := filepath.Join(codiveDir, "codive.log")
	file, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("failed to open log file %s: %w", logPath, err)
	}
	LogFileHandle = file

	var writer io.Writer = file
	level := slog.LevelInfo
	if verbose {
		writer = io.MultiWriter(file, os.Stderr)
		level = slog.LevelDebug
	}

	handler := slog.NewJSONHandler(writer, &slog.HandlerOptions{
		Level: level,
	})

	logger := slog.New(handler)
	slog.SetDefault(logger)

	return nil
}

// Close closes the underlying log file handle.
func Close() {
	if LogFileHandle != nil {
		_ = LogFileHandle.Close()
	}
}
