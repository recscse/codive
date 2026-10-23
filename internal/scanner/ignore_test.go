package scanner

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The scanner's .gitignore handling must agree with git itself: the files it
// indexes are exactly the files git doesn't ignore (minus codive's own
// built-in exclusions, which this fixture avoids apart from secrets).
func TestGitignoreMatchesGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
		return string(out)
	}
	git("init", "-q")

	write(".gitignore", strings.Join([]string{
		"# comment",
		"*.log",
		"!keep.log",
		"/out",
		"generated/",
		"docs/**/draft-*.md",
		"**/cache",
		"tmp?.txt",
		"[ab].cfg",
		"sub/local.txt",
		"trailing.txt   ",
	}, "\n")+"\n")
	write(".git/info/exclude", "excluded-locally.txt\n")
	write("pkg/.gitignore", "*.gen.go\n!important.gen.go\n/rootonly.txt\n")

	files := []string{
		"main.go", "app.log", "keep.log", "logs/deep/x.log",
		"out/bundle.js", "src/out/kept.js",
		"generated/a.go", "src/generated/b.go",
		"docs/guide.md", "docs/v1/draft-x.md", "docs/v1/v2/draft-y.md", "draft-z.md",
		"cache/c.txt", "a/b/cache/d.txt",
		"tmp1.txt", "tmp12.txt", "a.cfg", "c.cfg",
		"sub/local.txt", "other/sub/local.txt", "trailing.txt",
		"excluded-locally.txt",
		"pkg/x.gen.go", "pkg/important.gen.go", "pkg/rootonly.txt", "pkg/inner/rootonly.txt", "pkg/inner/y.gen.go",
	}
	for _, f := range files {
		write(f, "package x\n")
	}

	want := strings.Fields(git("ls-files", "--others", "--exclude-standard"))
	sort.Strings(want)

	res, err := Scan(root)
	if err != nil {
		t.Fatalf("scan failed: %v", err)
	}
	var got []string
	for _, f := range res.Files {
		got = append(got, f.Path)
	}
	sort.Strings(got)

	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("scanner disagrees with git.\n got: %v\nwant: %v", got, want)
	}
}

func TestIsSecretPath(t *testing.T) {
	secret := []string{".env", "config/.env.production", "certs/server.pem", "tls.key", "id_rsa", "home/.npmrc", "infra/terraform.tfstate", "store.jks", "gcp/credentials.json"}
	safe := []string{".env.example", ".env.sample", "main.go", "keys.go", "environment.ts", "README.md", "id_rsa_test.go"}
	for _, p := range secret {
		if !IsSecretPath(p) {
			t.Errorf("IsSecretPath(%q) = false, want true", p)
		}
	}
	for _, p := range safe {
		if IsSecretPath(p) {
			t.Errorf("IsSecretPath(%q) = true, want false", p)
		}
	}
}

// Secret-looking files must never be indexed, even without a .gitignore.
func TestScanSkipsSecretsWithoutGitignore(t *testing.T) {
	root := t.TempDir()
	for name, content := range map[string]string{
		".env":         "API_KEY=sk_live_x\n",
		".env.example": "API_KEY=\n",
		"server.pem":   "-----BEGIN PRIVATE KEY-----\n",
		"main.go":      "package main\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	res, err := Scan(root)
	if err != nil {
		t.Fatalf("scan failed: %v", err)
	}
	var got []string
	for _, f := range res.Files {
		got = append(got, f.Path)
	}
	sort.Strings(got)
	if strings.Join(got, ",") != ".env.example,main.go" {
		t.Errorf("expected only .env.example and main.go, got %v", got)
	}
}
