package cmd

import (
	"os"
	"path/filepath"
	"strings"
)

const (
	guidanceStart = "<!-- codive:start -->"
	guidanceEnd   = "<!-- codive:end -->"
)

// legacyAgentRule is the unmarked section older codive versions appended to
// instruction files; it's migrated into the marked block when found.
const legacyAgentRule = "## Code Search & Exploration Rules\n" +
	"- **DO NOT** use raw `grep`, `ripgrep`, or recursive `list_dir` for codebase exploration.\n" +
	"- **ALWAYS PREFER** the `codive` MCP tools:\n" +
	"  1. Use `codive:get_repo_map` to understand the codebase structure and symbols.\n" +
	"  2. Use `codive:find_symbol` when locating function, class, or type definitions.\n" +
	"  3. Use `codive:find_references` when discovering callers or usages of a function/type.\n" +
	"  4. Use `codive:search_code` when searching for terms or strings across files.\n" +
	"  5. Use `codive:read_file_context` to read files with AST symbol summaries.\n"

// guidanceTarget is one agent instruction file.
type guidanceTarget struct {
	Path string
	// Owned files contain nothing but codive's guidance, so they're created
	// whole and deleted on undo. Shared files (CLAUDE.md, AGENTS.md, ...)
	// only ever get codive's marked block added or removed.
	Owned  bool
	Header string // written before the block when codive creates an owned file
}

// ruleAction reports what happened to one instruction file.
type ruleAction struct {
	Path   string
	Status string
}

// guidanceTargets lists the instruction files to maintain: shared files that
// already exist, plus the file each detected client reads.
func guidanceTargets(project string, env setupEnv) []guidanceTarget {
	detected := make(map[string]bool)
	for _, t := range setupTargets(env) {
		if t.Detected {
			detected[t.Client] = true
		}
	}
	exists := func(rel string) bool {
		_, err := os.Stat(filepath.Join(project, filepath.FromSlash(rel)))
		return err == nil
	}

	var targets []guidanceTarget
	shared := []struct {
		rel    string
		client string // create the file when this client is detected
	}{
		{"AGENTS.md", "Codex CLI"},
		{"CLAUDE.md", "Claude Code"},
		{"GEMINI.md", "Gemini CLI"},
		{".windsurfrules", ""},
		{".cursorrules", ""},
		{".github/copilot-instructions.md", ""},
	}
	for _, s := range shared {
		if exists(s.rel) || (s.client != "" && detected[s.client]) {
			targets = append(targets, guidanceTarget{Path: filepath.Join(project, filepath.FromSlash(s.rel))})
		}
	}
	if detected["Cursor"] || exists(".cursor/rules/codive.mdc") {
		targets = append(targets, guidanceTarget{
			Path:   filepath.Join(project, ".cursor", "rules", "codive.mdc"),
			Owned:  true,
			Header: "---\ndescription: How to navigate this codebase with codive's MCP tools\nalwaysApply: true\n---\n",
		})
	}
	if detected["Windsurf"] || exists(".windsurf/rules/codive.md") {
		targets = append(targets, guidanceTarget{
			Path:  filepath.Join(project, ".windsurf", "rules", "codive.md"),
			Owned: true,
		})
	}
	return targets
}

// applyAgentRules installs (or removes) the codive usage guidance.
func applyAgentRules(project string, env setupEnv, dryRun, undo bool) []ruleAction {
	return writeAgentGuidance(project, env, codiveGuidance, dryRun, undo)
}

// writeAgentGuidance writes content as codive's marked block into every
// guidance target, or removes the block when undo is set.
func writeAgentGuidance(project string, env setupEnv, content string, dryRun, undo bool) []ruleAction {
	var actions []ruleAction
	for _, t := range guidanceTargets(project, env) {
		status, err := upsertGuidance(t, content, dryRun, undo)
		if err != nil {
			status = "failed (" + err.Error() + ")"
		}
		if status != "" {
			actions = append(actions, ruleAction{Path: t.Path, Status: status})
		}
	}
	return actions
}

func upsertGuidance(t guidanceTarget, content string, dryRun, undo bool) (string, error) {
	data, err := os.ReadFile(t.Path)
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	existed := err == nil
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	block := guidanceStart + "\n" + strings.TrimRight(content, "\n") + "\n" + guidanceEnd + "\n"

	// Migrate the unmarked section older versions appended.
	text = strings.Replace(text, legacyAgentRule, "", 1)

	var out, status string
	start := strings.Index(text, guidanceStart)
	end := strings.Index(text, guidanceEnd)
	hasBlock := start >= 0 && end > start

	switch {
	case undo && !hasBlock:
		return "", nil
	case undo:
		out = text[:start] + text[end+len(guidanceEnd):]
		out = strings.TrimLeft(strings.TrimRight(out, "\n")+"\n", "\n")
		status = "removed"
		if t.Owned || strings.TrimSpace(strings.TrimPrefix(out, t.Header)) == "" {
			if !dryRun {
				if err := os.Remove(t.Path); err != nil {
					return "", err
				}
			}
			return "removed", nil
		}
	case hasBlock:
		out = text[:start] + block + strings.TrimPrefix(text[end+len(guidanceEnd):], "\n")
		if out == text {
			return "unchanged", nil
		}
		status = "updated"
	case !existed:
		out = t.Header + block
		status = "created"
	default:
		out = strings.TrimRight(text, "\n") + "\n\n" + block
		status = "added"
	}

	if dryRun {
		return status, nil
	}
	if err := os.MkdirAll(filepath.Dir(t.Path), 0755); err != nil {
		return "", err
	}
	return status, os.WriteFile(t.Path, []byte(out), 0644)
}
