package cmd

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func testEnv(t *testing.T) setupEnv {
	t.Helper()
	return setupEnv{
		Home:       t.TempDir(),
		AppData:    t.TempDir(),
		GOOS:       "linux",
		Project:    t.TempDir(),
		Executable: "/opt/codive/codive",
		LookPath:   func(string) (string, error) { return "", errors.New("not found") },
	}
}

func mkdir(t *testing.T, parts ...string) string {
	t.Helper()
	p := filepath.Join(parts...)
	if err := os.MkdirAll(p, 0755); err != nil {
		t.Fatal(err)
	}
	return p
}

func actionFor(actions []setupAction, client string) *setupAction {
	for i := range actions {
		if actions[i].Target.Client == client {
			return &actions[i]
		}
	}
	return nil
}

// Setup must only touch codive's own entry: other servers (including their
// env secrets and remote settings), unrelated top-level settings, and key
// order must all survive, with a one-time backup of the original.
func TestSetupPreservesExistingConfig(t *testing.T) {
	env := testEnv(t)
	dir := mkdir(t, env.Home, ".config", "Claude")
	cfg := filepath.Join(dir, "claude_desktop_config.json")
	original := `{
  "globalShortcut": "Ctrl+Space",
  "mcpServers": {
    "github": {"command": "npx", "args": ["-y", "@x/github"], "env": {"GITHUB_TOKEN": "ghp_secret"}},
    "remote": {"type": "http", "url": "https://example.com/mcp", "headers": {"Authorization": "Bearer t"}}
  },
  "zzz": [1, 2]
}`
	if err := os.WriteFile(cfg, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}

	a := actionFor(applySetup(env, false, false), "Claude Desktop")
	if a == nil || a.Status != "added" {
		t.Fatalf("expected Claude Desktop added, got %+v", a)
	}

	data, _ := os.ReadFile(cfg)
	var got struct {
		GlobalShortcut string `json:"globalShortcut"`
		MCPServers     map[string]struct {
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
			Type    string            `json:"type"`
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
		Zzz []int `json:"zzz"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("result is not valid JSON: %v\n%s", err, data)
	}
	if got.GlobalShortcut != "Ctrl+Space" || len(got.Zzz) != 2 {
		t.Errorf("top-level settings lost: %s", data)
	}
	if got.MCPServers["github"].Env["GITHUB_TOKEN"] != "ghp_secret" {
		t.Errorf("other server's env was dropped: %s", data)
	}
	if r := got.MCPServers["remote"]; r.URL != "https://example.com/mcp" || r.Headers["Authorization"] != "Bearer t" || r.Type != "http" {
		t.Errorf("remote server fields were dropped: %s", data)
	}
	if c := got.MCPServers["codive"]; c.Command != env.Executable || len(c.Args) != 2 || c.Args[1] != env.Project {
		t.Errorf("codive entry wrong: %+v", c)
	}
	text := string(data)
	if !(strings.Index(text, "globalShortcut") < strings.Index(text, "mcpServers") && strings.Index(text, "mcpServers") < strings.Index(text, "zzz")) {
		t.Errorf("key order not preserved:\n%s", text)
	}
	if backup, err := os.ReadFile(cfg + ".codive-backup"); err != nil || string(backup) != original {
		t.Errorf("backup missing or different: %v", err)
	}

	if a := actionFor(applySetup(env, false, false), "Claude Desktop"); a == nil || a.Status != "unchanged" {
		t.Errorf("second run should be a no-op, got %+v", a)
	}

	if a := actionFor(applySetup(env, false, true), "Claude Desktop"); a == nil || a.Status != "removed" {
		t.Fatalf("undo should remove the entry, got %+v", a)
	}
	var afterUndo, before any
	data, _ = os.ReadFile(cfg)
	_ = json.Unmarshal(data, &afterUndo)
	_ = json.Unmarshal([]byte(original), &before)
	a1, _ := json.Marshal(afterUndo)
	b1, _ := json.Marshal(before)
	if string(a1) != string(b1) {
		t.Errorf("undo did not restore the original content:\n got %s\nwant %s", a1, b1)
	}
}

// A config with comments can't be rewritten without losing them, so it must
// be left untouched and reported for manual editing.
func TestSetupLeavesJSONCUntouched(t *testing.T) {
	env := testEnv(t)
	dir := mkdir(t, env.Home, ".codeium", "windsurf")
	cfg := filepath.Join(dir, "mcp_config.json")
	original := "{\n  // my servers\n  \"mcpServers\": {},\n}\n"
	if err := os.WriteFile(cfg, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}
	a := actionFor(applySetup(env, false, false), "Windsurf")
	if a == nil || a.Status != "manual" || !strings.Contains(a.Detail, `"codive"`) {
		t.Fatalf("expected manual instructions, got %+v", a)
	}
	if data, _ := os.ReadFile(cfg); string(data) != original {
		t.Errorf("JSONC config was modified:\n%s", data)
	}
}

// Nothing may be created for clients that aren't installed.
func TestSetupSkipsUndetectedClients(t *testing.T) {
	env := testEnv(t)
	if actions := applySetup(env, false, false); len(actions) != 0 {
		t.Errorf("expected no actions with no clients installed, got %+v", actions)
	}
	for _, dir := range []string{env.Home, env.Project} {
		entries, _ := os.ReadDir(dir)
		if len(entries) != 0 {
			t.Errorf("setup created files in %s: %v", dir, entries)
		}
	}
}

func TestSetupClientFormats(t *testing.T) {
	env := testEnv(t)
	mkdir(t, env.Home, ".config", "Code", "User")
	mkdir(t, env.Home, ".cursor")
	mkdir(t, env.Home, ".codex")
	if err := os.WriteFile(filepath.Join(env.Home, ".codex", "config.toml"), []byte("model = \"o4\"\n\n[mcp_servers.other]\ncommand = \"x\"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	applySetup(env, false, false)

	var vscode struct {
		Servers map[string]struct {
			Type string   `json:"type"`
			Args []string `json:"args"`
		} `json:"servers"`
	}
	data, err := os.ReadFile(filepath.Join(env.Project, ".vscode", "mcp.json"))
	if err != nil || json.Unmarshal(data, &vscode) != nil {
		t.Fatalf("VS Code config missing or invalid: %v %s", err, data)
	}
	if c := vscode.Servers["codive"]; c.Type != "stdio" || len(c.Args) != 2 || c.Args[1] != "${workspaceFolder}" {
		t.Errorf("VS Code entry must use servers/type stdio/${workspaceFolder}: %s", data)
	}

	if _, err := os.Stat(filepath.Join(env.Project, ".cursor", "mcp.json")); err != nil {
		t.Errorf("Cursor should be configured per project: %v", err)
	}

	toml, _ := os.ReadFile(filepath.Join(env.Home, ".codex", "config.toml"))
	if !strings.Contains(string(toml), "[mcp_servers.other]") || !strings.Contains(string(toml), "model = \"o4\"") || !strings.Contains(string(toml), codexSectionHeader) {
		t.Errorf("Codex config not merged correctly:\n%s", toml)
	}
	applySetup(env, false, true)
	toml, _ = os.ReadFile(filepath.Join(env.Home, ".codex", "config.toml"))
	if strings.Contains(string(toml), codexSectionHeader) || !strings.Contains(string(toml), "[mcp_servers.other]") {
		t.Errorf("Codex undo wrong:\n%s", toml)
	}
}

func TestDryRunWritesNothing(t *testing.T) {
	env := testEnv(t)
	mkdir(t, env.Home, ".cursor")
	actions := applySetup(env, true, false)
	if a := actionFor(actions, "Cursor"); a == nil || a.Status != "added" {
		t.Errorf("dry run should report the planned change, got %+v", actions)
	}
	if _, err := os.Stat(filepath.Join(env.Project, ".cursor")); !os.IsNotExist(err) {
		t.Error("dry run wrote files")
	}
}

// Agent guidance must only ever edit codive's marked block, keep the user's
// own instructions, migrate the legacy unmarked section, and undo cleanly.
func TestAgentGuidanceKeepsUserContent(t *testing.T) {
	env := testEnv(t)
	claude := filepath.Join(env.Project, "CLAUDE.md")
	user := "# My project\n\nAlways use tabs.\n"
	if err := os.WriteFile(claude, []byte(user+"\n\n"+legacyAgentRule), 0644); err != nil {
		t.Fatal(err)
	}

	applyAgentRules(env.Project, env, false, false)
	data, _ := os.ReadFile(claude)
	text := string(data)
	if !strings.HasPrefix(text, user) || !strings.Contains(text, guidanceStart) || strings.Contains(text, "DO NOT") {
		t.Fatalf("guidance not added cleanly:\n%s", text)
	}

	if actions := applyAgentRules(env.Project, env, false, false); len(actions) != 1 || actions[0].Status != "unchanged" {
		t.Errorf("rerun should be unchanged, got %+v", actions)
	}

	writeAgentGuidance(env.Project, env, generateRulesMarkdown(detectProjectStack(env.Project)), false, false)
	data, _ = os.ReadFile(claude)
	if strings.Count(string(data), guidanceStart) != 1 || !strings.Contains(string(data), "Project Overview") || !strings.HasPrefix(string(data), user) {
		t.Errorf("init-rules should replace the block in place:\n%s", data)
	}

	applyAgentRules(env.Project, env, false, true)
	data, _ = os.ReadFile(claude)
	if strings.TrimSpace(string(data)) != strings.TrimSpace(user) {
		t.Errorf("undo should leave only the user's content, got:\n%s", data)
	}
}

func TestAgentGuidanceCreatesOnlyForDetectedClients(t *testing.T) {
	env := testEnv(t)
	mkdir(t, env.Home, ".cursor")
	actions := applyAgentRules(env.Project, env, false, false)
	if len(actions) != 1 || !strings.HasSuffix(filepath.ToSlash(actions[0].Path), ".cursor/rules/codive.mdc") {
		t.Fatalf("expected only the Cursor rule file, got %+v", actions)
	}
	for _, f := range []string{"CLAUDE.md", "AGENTS.md", "GEMINI.md", ".cursorrules", ".windsurfrules"} {
		if _, err := os.Stat(filepath.Join(env.Project, f)); !os.IsNotExist(err) {
			t.Errorf("%s was created without its client being installed", f)
		}
	}
	applyAgentRules(env.Project, env, false, true)
	if _, err := os.Stat(actions[0].Path); !os.IsNotExist(err) {
		t.Error("undo should delete the codive-owned rule file")
	}
}

// Hooks must be added to existing hooks rather than replacing them, follow
// core.hooksPath, and undo back to the original content.
func TestInstallHooksKeepsExistingHooks(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"config", "core.hooksPath", ".githooks"}} {
		c := exec.Command("git", args...)
		c.Dir = repo
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	hooks := mkdir(t, repo, ".githooks")
	userHook := "#!/bin/sh\nnpm run lint-staged\n"
	if err := os.WriteFile(filepath.Join(hooks, "post-commit"), []byte(userHook), 0755); err != nil {
		t.Fatal(err)
	}

	if err := RunInstallHooks(repo, false, false); err != nil {
		t.Fatalf("install failed: %v", err)
	}
	pc, _ := os.ReadFile(filepath.Join(hooks, "post-commit"))
	if !strings.HasPrefix(string(pc), userHook) || !strings.Contains(string(pc), hookBody) {
		t.Errorf("existing hook not preserved:\n%s", pc)
	}
	if _, err := os.Stat(filepath.Join(hooks, "post-checkout")); err != nil {
		t.Errorf("post-checkout not created in core.hooksPath: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, ".git", "hooks", "post-commit")); err == nil {
		t.Error("hook written to .git/hooks despite core.hooksPath")
	}

	if err := RunInstallHooks(repo, false, true); err != nil {
		t.Fatalf("undo failed: %v", err)
	}
	pc, _ = os.ReadFile(filepath.Join(hooks, "post-commit"))
	if strings.TrimSpace(string(pc)) != strings.TrimSpace(userHook) {
		t.Errorf("undo should restore the user's hook, got:\n%s", pc)
	}
	if _, err := os.Stat(filepath.Join(hooks, "post-checkout")); !os.IsNotExist(err) {
		t.Error("undo should delete hooks codive created")
	}
}

func TestIsRegistered(t *testing.T) {
	env := testEnv(t)
	mkdir(t, env.Home, ".cursor")
	mkdir(t, env.Home, ".codex")
	for _, target := range setupTargets(env) {
		if target.Detected && isRegistered(target) {
			t.Errorf("%s reported registered before setup", target.Client)
		}
	}
	applySetup(env, false, false)
	for _, target := range setupTargets(env) {
		if target.Detected && !isRegistered(target) {
			t.Errorf("%s not reported registered after setup", target.Client)
		}
	}
}
