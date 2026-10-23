// Package cmd implements the command line actions and subcommands for codive.
package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/recscse/codive/internal/ui"
)

const (
	hookStart = "# codive:start"
	hookEnd   = "# codive:end"
	// hookBody re-indexes in the background and never changes the hook's
	// exit status, so it can't block or fail a commit or checkout.
	hookBody = "command -v codive >/dev/null 2>&1 && (codive update >/dev/null 2>&1 &)"
)

var syncHooks = []string{"post-commit", "post-checkout", "post-merge"}

// RunInstallHooks adds a background re-index step to the repository's git
// hooks. Existing hooks are kept: codive's lines go in a marked block that
// undo removes again.
func RunInstallHooks(targetDir string, dryRun, undo bool) error {
	absDir, err := filepath.Abs(targetDir)
	if err != nil {
		return fmt.Errorf("invalid directory path: %w", err)
	}
	hooksDir, err := gitHooksDir(absDir)
	if err != nil {
		return err
	}

	ui.Header("🪝 Git Hooks")
	fmt.Println()
	ui.KeyValue("Hooks directory", hooksDir)
	if rel, err := filepath.Rel(absDir, hooksDir); err == nil && !strings.HasPrefix(rel, "..") && !strings.HasPrefix(rel, ".git") {
		ui.Warning("Hooks live inside the working tree (core.hooksPath). If a tool such as husky regenerates them, add this line to your hook source instead:\n  " + hookBody)
	}
	fmt.Println()

	for _, name := range syncHooks {
		status, err := upsertHook(filepath.Join(hooksDir, name), dryRun, undo)
		switch {
		case err != nil:
			ui.Warning(fmt.Sprintf("%s: %v", name, err))
		case status != "":
			fmt.Printf("  %s %s\n", ui.GreenBold.Sprint("✓ "+status+":"), ui.Bold.Sprint(name))
		}
	}
	fmt.Println()
	if !undo && !dryRun {
		ui.Success("codive will re-index in the background after commits, checkouts, and merges.")
	}
	return nil
}

// gitHooksDir asks git where hooks live, which accounts for core.hooksPath
// and linked worktrees.
func gitHooksDir(repo string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "--git-path", "hooks")
	cmd.Dir = repo
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("not a git repository (or git is not installed): %s", repo)
	}
	dir := strings.TrimSpace(string(out))
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(repo, dir)
	}
	return dir, nil
}

func upsertHook(path string, dryRun, undo bool) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	existed := err == nil
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	block := hookStart + "\n" + hookBody + "\n" + hookEnd + "\n"

	start := strings.Index(text, hookStart)
	end := strings.Index(text, hookEnd)
	hasBlock := start >= 0 && end > start

	var out, status string
	switch {
	case undo && !hasBlock:
		return "", nil
	case undo:
		out = text[:start] + strings.TrimPrefix(text[end+len(hookEnd):], "\n")
		status = "removed"
		if strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(out), "#!/bin/sh")) == "" {
			// Only codive's lines were left: the hook was ours, delete it.
			if !dryRun {
				return status, os.Remove(path)
			}
			return status, nil
		}
	case hasBlock:
		out = text[:start] + block + strings.TrimPrefix(text[end+len(hookEnd):], "\n")
		if out == text {
			return "unchanged", nil
		}
		status = "updated"
	case !existed:
		out = "#!/bin/sh\n" + block
		status = "created"
	default:
		first := strings.SplitN(text, "\n", 2)[0]
		if strings.HasPrefix(first, "#!") && !strings.Contains(first, "sh") {
			return "", fmt.Errorf("existing hook is not a shell script (%s); add this line to it yourself: %s", first, hookBody)
		}
		out = strings.TrimRight(text, "\n") + "\n\n" + block
		status = "added to existing hook"
	}
	if dryRun {
		return status, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return "", err
	}
	return status, os.WriteFile(path, []byte(out), 0755)
}
