// Package cmd implements the command line actions and subcommands for codive.
package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/recscse/codive/internal/ui"
)

// mcpServerName is the key codive registers itself under in client configs.
const mcpServerName = "codive"

// readOnlyTools are safe to auto-approve in clients that support it: they
// only read the index or the working tree.
var readOnlyTools = []string{
	"get_repo_map", "find_symbol", "find_references", "find_callers", "find_callees",
	"find_tests_for", "get_file_skeleton", "read_file_context", "read_symbol", "search_code",
	"pack_feature_context", "blast_radius", "get_git_changes", "get_decisions",
}

// setupEnv holds everything setup reads from the machine, so tests can point
// it at a temporary home directory.
type setupEnv struct {
	Home       string
	AppData    string // Windows %APPDATA%
	GOOS       string
	Project    string // absolute path of the repository being set up
	Executable string // command used to launch codive
	LookPath   func(string) (string, error)
}

func defaultSetupEnv(project string) (setupEnv, error) {
	exe, err := os.Executable()
	if err != nil {
		return setupEnv{}, fmt.Errorf("failed to determine codive executable path: %w", err)
	}
	exe, _ = filepath.Abs(exe)
	home, err := os.UserHomeDir()
	if err != nil {
		return setupEnv{}, fmt.Errorf("failed to find user home directory: %w", err)
	}
	appData := os.Getenv("APPDATA")
	if appData == "" {
		appData = filepath.Join(home, "AppData", "Roaming")
	}
	return setupEnv{
		Home: home, AppData: appData, GOOS: runtime.GOOS, Project: project,
		Executable: exe, LookPath: exec.LookPath,
	}, nil
}

// mcpTarget is one client configuration file codive can register itself in.
type mcpTarget struct {
	Client     string
	Path       string
	Format     string // "json" or "codex-toml"
	ServersKey string // top-level key holding servers ("mcpServers" or "servers")
	Entry      map[string]any
	Detected   bool
	Scope      string // "project" or "global"
}

func dirExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

// vscodeUserDir returns the VS Code user settings directory for the platform.
func vscodeUserDir(env setupEnv) string {
	switch env.GOOS {
	case "windows":
		return filepath.Join(env.AppData, "Code", "User")
	case "darwin":
		return filepath.Join(env.Home, "Library", "Application Support", "Code", "User")
	default:
		return filepath.Join(env.Home, ".config", "Code", "User")
	}
}

func claudeDesktopDir(env setupEnv) string {
	switch env.GOOS {
	case "windows":
		return filepath.Join(env.AppData, "Claude")
	case "darwin":
		return filepath.Join(env.Home, "Library", "Application Support", "Claude")
	default:
		return filepath.Join(env.Home, ".config", "Claude")
	}
}

// setupTargets lists every supported client with whether it's installed.
// Clients with a per-project config are registered per project, so each
// repository gets its own codive server instead of every project being
// answered from whichever repository setup was last run in.
func setupTargets(env setupEnv) []mcpTarget {
	command := env.Executable
	if p, err := env.LookPath("codive"); err == nil && sameFile(p, env.Executable) {
		// On PATH: use the bare name so project config files that get
		// committed don't embed one machine's install location.
		command = "codive"
	}
	stdio := func(extra map[string]any) map[string]any {
		e := map[string]any{"command": command, "args": []string{"serve", env.Project}}
		for k, v := range extra {
			e[k] = v
		}
		return e
	}
	_, claudeOnPath := env.LookPath("claude")
	codeStorage := filepath.Join(vscodeUserDir(env), "globalStorage")

	return []mcpTarget{
		{
			Client: "Claude Code", Scope: "project", Format: "json", ServersKey: "mcpServers",
			Path:     filepath.Join(env.Project, ".mcp.json"),
			Entry:    stdio(nil),
			Detected: claudeOnPath == nil || dirExists(filepath.Join(env.Home, ".claude")),
		},
		{
			Client: "Cursor", Scope: "project", Format: "json", ServersKey: "mcpServers",
			Path:     filepath.Join(env.Project, ".cursor", "mcp.json"),
			Entry:    stdio(nil),
			Detected: dirExists(filepath.Join(env.Home, ".cursor")),
		},
		{
			Client: "VS Code (Copilot agent mode)", Scope: "project", Format: "json", ServersKey: "servers",
			Path: filepath.Join(env.Project, ".vscode", "mcp.json"),
			// VS Code expands ${workspaceFolder} itself, keeping the file portable.
			Entry:    map[string]any{"type": "stdio", "command": command, "args": []string{"serve", "${workspaceFolder}"}},
			Detected: dirExists(vscodeUserDir(env)),
		},
		{
			Client: "Claude Desktop", Scope: "global", Format: "json", ServersKey: "mcpServers",
			Path:     filepath.Join(claudeDesktopDir(env), "claude_desktop_config.json"),
			Entry:    stdio(nil),
			Detected: dirExists(claudeDesktopDir(env)),
		},
		{
			Client: "Windsurf", Scope: "global", Format: "json", ServersKey: "mcpServers",
			Path:     filepath.Join(env.Home, ".codeium", "windsurf", "mcp_config.json"),
			Entry:    stdio(nil),
			Detected: dirExists(filepath.Join(env.Home, ".codeium", "windsurf")),
		},
		{
			Client: "Gemini CLI", Scope: "global", Format: "json", ServersKey: "mcpServers",
			Path:     filepath.Join(env.Home, ".gemini", "settings.json"),
			Entry:    stdio(nil),
			Detected: dirExists(filepath.Join(env.Home, ".gemini")),
		},
		{
			Client: "Google Antigravity", Scope: "global", Format: "json", ServersKey: "mcpServers",
			Path:     filepath.Join(env.Home, ".gemini", "config", "mcp_config.json"),
			Entry:    stdio(nil),
			Detected: dirExists(filepath.Join(env.Home, ".gemini", "config")),
		},
		{
			Client: "Cline (VS Code)", Scope: "global", Format: "json", ServersKey: "mcpServers",
			Path:     filepath.Join(codeStorage, "saoudrizwan.claude-dev", "settings", "cline_mcp_settings.json"),
			Entry:    stdio(map[string]any{"autoApprove": readOnlyTools}),
			Detected: dirExists(filepath.Join(codeStorage, "saoudrizwan.claude-dev")),
		},
		{
			Client: "Roo Code (VS Code)", Scope: "global", Format: "json", ServersKey: "mcpServers",
			Path:     filepath.Join(codeStorage, "rooveterinaryinc.roo-cline", "settings", "cline_mcp_settings.json"),
			Entry:    stdio(map[string]any{"autoApprove": readOnlyTools}),
			Detected: dirExists(filepath.Join(codeStorage, "rooveterinaryinc.roo-cline")),
		},
		{
			Client: "Codex CLI", Scope: "global", Format: "codex-toml",
			Path:     filepath.Join(env.Home, ".codex", "config.toml"),
			Entry:    stdio(nil),
			Detected: dirExists(filepath.Join(env.Home, ".codex")),
		},
	}
}

func sameFile(a, b string) bool {
	ai, err1 := os.Stat(a)
	bi, err2 := os.Stat(b)
	return err1 == nil && err2 == nil && os.SameFile(ai, bi)
}

// setupAction describes the outcome for one target.
type setupAction struct {
	Target mcpTarget
	Status string // "added", "updated", "unchanged", "removed", "skipped", "manual"
	Detail string
}

// errNeedsManualEdit means a config file exists but can't be edited without
// risking the user's content (comments, trailing commas, invalid JSON).
var errNeedsManualEdit = errors.New("needs manual edit")

// RunSetup registers codive with every detected AI client and installs agent
// guidance. dryRun reports what would change without writing; undo removes
// what setup added.
func RunSetup(targetDir string, dryRun, undo bool) error {
	absTarget, err := filepath.Abs(targetDir)
	if err != nil {
		return fmt.Errorf("invalid directory path: %w", err)
	}
	env, err := defaultSetupEnv(absTarget)
	if err != nil {
		return err
	}

	title := "codive — AI Client MCP Configuration Setup"
	if undo {
		title = "codive — Removing MCP Configuration"
	}
	ui.Header(title)
	ui.Divider()
	ui.KeyValue("Executable", env.Executable)
	ui.KeyValue("Target Repo", absTarget)
	if dryRun {
		ui.KeyValue("Mode", "dry run (no files are changed)")
	}
	ui.Divider()
	fmt.Println()

	actions := applySetup(env, dryRun, undo)
	printSetupActions(actions)

	ruleActions := applyAgentRules(absTarget, env, dryRun, undo)
	for _, a := range ruleActions {
		fmt.Printf("  %s %s\n", ui.GreenBold.Sprint("✔ Rule "+a.Status+":"), ui.Dim.Sprint(a.Path))
	}

	if dirExists(filepath.Join(env.Home, ".continue")) && !undo {
		fmt.Println()
		ui.Warning("Continue detected: its MCP config format differs per version, so it isn't edited automatically.")
		fmt.Printf("  Add a stdio MCP server running: %s serve %s\n", env.Executable, absTarget)
	}
	fmt.Println()
	return nil
}

func printSetupActions(actions []setupAction) {
	configured := 0
	for _, a := range actions {
		switch a.Status {
		case "added", "updated", "removed", "unchanged":
			configured++
			fmt.Printf("  %s %s %s (%s)\n", ui.GreenBold.Sprint("✔"), ui.Bold.Sprint(a.Target.Client), ui.Dim.Sprint(a.Status), ui.Dim.Sprint(a.Target.Path))
		case "manual":
			ui.Warning(fmt.Sprintf("%s: %s", a.Target.Client, a.Detail))
		}
	}
	if configured == 0 {
		ui.Warning("No supported AI clients were detected on this machine.")
	}
}

// applySetup adds (or with undo, removes) the codive entry in every detected
// client's config.
func applySetup(env setupEnv, dryRun, undo bool) []setupAction {
	var actions []setupAction
	for _, t := range setupTargets(env) {
		if !t.Detected {
			continue
		}
		var status string
		var err error
		switch t.Format {
		case "codex-toml":
			status, err = editCodexConfig(t, dryRun, undo)
		default:
			status, err = editJSONConfig(t, dryRun, undo)
		}
		a := setupAction{Target: t, Status: status}
		if errors.Is(err, errNeedsManualEdit) {
			entry, _ := json.MarshalIndent(map[string]any{mcpServerName: t.Entry}, "  ", "  ")
			a.Status = "manual"
			a.Detail = fmt.Sprintf("%s isn't plain JSON (comments or trailing commas?), so it was left untouched. Add this under %q:\n  %s", t.Path, t.ServersKey, entry)
		} else if err != nil {
			a.Status = "manual"
			a.Detail = err.Error()
		}
		if a.Status != "skipped" {
			actions = append(actions, a)
		}
	}
	return actions
}

// orderedObject is a JSON object whose keys keep their original order and
// whose values are kept verbatim, so editing one entry can't drop or
// reformat anything codive doesn't understand (env vars, other servers,
// unrelated settings).
type orderedObject []orderedField

type orderedField struct {
	Key   string
	Value json.RawMessage
}

func parseOrderedObject(data []byte) (orderedObject, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, fmt.Errorf("top level is not a JSON object")
	}
	var obj orderedObject
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, ok := keyTok.(string)
		if !ok {
			return nil, fmt.Errorf("invalid object key")
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, err
		}
		obj = append(obj, orderedField{Key: key, Value: raw})
	}
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	if dec.More() {
		return nil, fmt.Errorf("trailing data after JSON object")
	}
	return obj, nil
}

func (o orderedObject) get(key string) (json.RawMessage, bool) {
	for _, f := range o {
		if f.Key == key {
			return f.Value, true
		}
	}
	return nil, false
}

func (o orderedObject) set(key string, v json.RawMessage) orderedObject {
	for i := range o {
		if o[i].Key == key {
			o[i].Value = v
			return o
		}
	}
	return append(o, orderedField{Key: key, Value: v})
}

func (o orderedObject) remove(key string) orderedObject {
	out := o[:0]
	for _, f := range o {
		if f.Key != key {
			out = append(out, f)
		}
	}
	return out
}

func (o orderedObject) marshal() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, f := range o {
		if i > 0 {
			buf.WriteByte(',')
		}
		k, _ := json.Marshal(f.Key)
		buf.Write(k)
		buf.WriteByte(':')
		buf.Write(f.Value)
	}
	buf.WriteByte('}')
	var out bytes.Buffer
	if err := json.Indent(&out, buf.Bytes(), "", "  "); err != nil {
		return nil, err
	}
	out.WriteByte('\n')
	return out.Bytes(), nil
}

// editJSONConfig adds, updates, or removes codive's server entry.
func editJSONConfig(t mcpTarget, dryRun, undo bool) (string, error) {
	data, err := os.ReadFile(t.Path)
	exists := err == nil
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	if !exists && undo {
		return "skipped", nil
	}

	root := orderedObject{}
	if exists && len(bytes.TrimSpace(data)) > 0 {
		if root, err = parseOrderedObject(data); err != nil {
			return "", errNeedsManualEdit
		}
	}
	servers := orderedObject{}
	if raw, ok := root.get(t.ServersKey); ok {
		if servers, err = parseOrderedObject(raw); err != nil {
			return "", errNeedsManualEdit
		}
	}

	existing, had := servers.get(mcpServerName)
	var status string
	if undo {
		if !had {
			return "skipped", nil
		}
		servers = servers.remove(mcpServerName)
		status = "removed"
	} else {
		entry, _ := json.Marshal(t.Entry)
		if had && jsonEqual(existing, entry) {
			return "unchanged", nil
		}
		servers = servers.set(mcpServerName, entry)
		status = "added"
		if had {
			status = "updated"
		}
	}

	serversJSON, err := servers.marshal()
	if err != nil {
		return "", err
	}
	root = root.set(t.ServersKey, bytes.TrimSpace(serversJSON))
	out, err := root.marshal()
	if err != nil {
		return "", err
	}
	if dryRun {
		return status, nil
	}
	return status, writeWithBackup(t.Path, data, exists, out)
}

func jsonEqual(a, b []byte) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	xb, _ := json.Marshal(x)
	yb, _ := json.Marshal(y)
	return bytes.Equal(xb, yb)
}

// writeWithBackup writes content to path, first saving the original next to
// it as <path>.codive-backup. The backup is only created once, so it always
// holds the file as it was before codive ever touched it.
func writeWithBackup(path string, original []byte, existed bool, content []byte) error {
	if existed {
		backup := path + ".codive-backup"
		if _, err := os.Stat(backup); os.IsNotExist(err) {
			if err := os.WriteFile(backup, original, 0600); err != nil {
				return fmt.Errorf("failed to back up %s: %w", path, err)
			}
		}
	} else if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, content, 0644)
}

const codexSectionHeader = "[mcp_servers.codive]"

// editCodexConfig manages codive's section in Codex's TOML config by text:
// the section is appended as a self-contained block and removed the same way,
// leaving every other line untouched.
func editCodexConfig(t mcpTarget, dryRun, undo bool) (string, error) {
	data, err := os.ReadFile(t.Path)
	exists := err == nil
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	text := string(data)
	has := strings.Contains(text, codexSectionHeader)

	args, _ := json.Marshal(t.Entry["args"])
	command, _ := json.Marshal(t.Entry["command"])
	block := fmt.Sprintf("%s\ncommand = %s\nargs = %s\n", codexSectionHeader, command, args)

	var out, status string
	switch {
	case undo && !has:
		return "skipped", nil
	case undo:
		out, status = removeTOMLSection(text, codexSectionHeader), "removed"
	case has:
		updated := removeTOMLSection(text, codexSectionHeader)
		out = strings.TrimRight(updated, "\n") + "\n\n" + block
		if out == text {
			return "unchanged", nil
		}
		status = "updated"
	default:
		sep := ""
		if len(strings.TrimSpace(text)) > 0 {
			sep = strings.TrimRight(text, "\n") + "\n\n"
		}
		out, status = sep+block, "added"
	}
	if dryRun {
		return status, nil
	}
	return status, writeWithBackup(t.Path, data, exists, []byte(out))
}

// removeTOMLSection deletes a [section] header and its key lines, up to the
// next section header.
func removeTOMLSection(text, header string) string {
	lines := strings.Split(text, "\n")
	var out []string
	skipping := false
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if trimmed == header {
			skipping = true
			continue
		}
		if skipping && strings.HasPrefix(trimmed, "[") {
			skipping = false
		}
		if !skipping {
			out = append(out, l)
		}
	}
	return strings.TrimRight(strings.Join(out, "\n"), "\n") + "\n"
}

// isRegistered reports whether a client's config already contains codive.
func isRegistered(t mcpTarget) bool {
	data, err := os.ReadFile(t.Path)
	if err != nil {
		return false
	}
	if t.Format == "codex-toml" {
		return strings.Contains(string(data), codexSectionHeader)
	}
	root, err := parseOrderedObject(data)
	if err != nil {
		return false
	}
	raw, ok := root.get(t.ServersKey)
	if !ok {
		return false
	}
	servers, err := parseOrderedObject(raw)
	if err != nil {
		return false
	}
	_, ok = servers.get(mcpServerName)
	return ok
}
