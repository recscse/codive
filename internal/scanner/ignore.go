package scanner

import (
	"bufio"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// secretFileNames and secretFilePatterns name files that commonly hold
// credentials. They are never indexed, even when no .gitignore covers them,
// because anything in the index can be returned to an AI agent (and from
// there to a model provider) through search_code or find_references.
var secretFileNames = map[string]bool{
	".env":             true,
	".netrc":           true,
	".npmrc":           true,
	".pypirc":          true,
	".git-credentials": true,
	"credentials":      true,
	"credentials.json": true,
	"id_rsa":           true,
	"id_dsa":           true,
	"id_ecdsa":         true,
	"id_ed25519":       true,
}

var secretFilePatterns = []string{
	".env.*", "*.pem", "*.key", "*.p12", "*.pfx", "*.jks", "*.keystore",
	"*.tfstate", "*.tfstate.*", "*.kdbx",
}

// secretFileExceptions are template files that document variables without
// real values and are useful context for an agent.
var secretFileExceptions = map[string]bool{
	".env.example":  true,
	".env.sample":   true,
	".env.template": true,
	".env.dist":     true,
}

// IsSecretPath reports whether a file looks like it holds credentials, based
// on its name. Such files are excluded from the index and refused by the MCP
// file-reading tools.
func IsSecretPath(relPath string) bool {
	base := strings.ToLower(path.Base(filepath.ToSlash(relPath)))
	if secretFileExceptions[base] {
		return false
	}
	if secretFileNames[base] {
		return true
	}
	for _, p := range secretFilePatterns {
		if ok, _ := path.Match(p, base); ok {
			return true
		}
	}
	return false
}

// gitignoreRule is one compiled line of a .gitignore file.
type gitignoreRule struct {
	base    string // directory holding the .gitignore, slash-separated, "" for root
	re      *regexp.Regexp
	negate  bool
	dirOnly bool
}

// gitignoreMatcher evaluates .gitignore rules for paths under a root. Rules
// from each directory's .gitignore are loaded lazily the first time a path
// below that directory is checked.
type gitignoreMatcher struct {
	root  string
	rules map[string][]gitignoreRule // keyed by directory rel path ("" = root)
}

func newGitignoreMatcher(root string) *gitignoreMatcher {
	m := &gitignoreMatcher{root: root, rules: make(map[string][]gitignoreRule)}
	// Repository-local excludes apply like a root .gitignore with lower priority.
	m.rules[""] = append(loadGitignore(filepath.Join(root, ".git", "info", "exclude"), ""),
		loadGitignore(filepath.Join(root, ".gitignore"), "")...)
	return m
}

func (m *gitignoreMatcher) rulesFor(dir string) []gitignoreRule {
	if r, ok := m.rules[dir]; ok {
		return r
	}
	r := loadGitignore(filepath.Join(m.root, filepath.FromSlash(dir), ".gitignore"), dir)
	m.rules[dir] = r
	return r
}

// Ignored reports whether relPath (slash-separated, relative to the root) is
// excluded. As in git, rules in deeper .gitignore files take precedence over
// shallower ones and later lines over earlier ones. Callers must not descend
// into ignored directories: like git, a file inside an excluded directory
// can't be re-included.
func (m *gitignoreMatcher) Ignored(relPath string, isDir bool) bool {
	dirs := []string{""}
	for i := 0; i < len(relPath); i++ {
		if relPath[i] == '/' {
			dirs = append(dirs, relPath[:i])
		}
	}
	ignored := false
	for _, d := range dirs {
		for _, r := range m.rulesFor(d) {
			if r.dirOnly && !isDir {
				continue
			}
			sub := relPath
			if r.base != "" {
				sub = strings.TrimPrefix(relPath, r.base+"/")
			}
			if r.re.MatchString(sub) {
				ignored = !r.negate
			}
		}
	}
	return ignored
}

func loadGitignore(file, base string) []gitignoreRule {
	f, err := os.Open(file)
	if err != nil {
		return nil
	}
	defer f.Close()
	var rules []gitignoreRule
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if r, ok := compileGitignoreLine(sc.Text(), base); ok {
			rules = append(rules, r)
		}
	}
	return rules
}

func compileGitignoreLine(line, base string) (gitignoreRule, bool) {
	line = strings.TrimRight(line, "\r")
	// Trailing spaces are ignored unless escaped.
	for strings.HasSuffix(line, " ") && !strings.HasSuffix(line, `\ `) {
		line = line[:len(line)-1]
	}
	if line == "" || strings.HasPrefix(line, "#") {
		return gitignoreRule{}, false
	}
	r := gitignoreRule{base: base}
	if strings.HasPrefix(line, "!") {
		r.negate = true
		line = line[1:]
	} else if strings.HasPrefix(line, `\!`) || strings.HasPrefix(line, `\#`) {
		line = line[1:]
	}
	if strings.HasSuffix(line, "/") {
		r.dirOnly = true
		line = strings.TrimRight(line, "/")
	}
	if line == "" {
		return gitignoreRule{}, false
	}
	// A slash at the start or in the middle anchors the pattern to the
	// .gitignore's directory; otherwise it matches a name at any depth.
	anchored := strings.Contains(line, "/")
	line = strings.TrimPrefix(line, "/")

	expr := globToRegexp(line)
	if !anchored {
		expr = "(?:.*/)?" + expr
	}
	re, err := regexp.Compile("^" + expr + "$")
	if err != nil {
		return gitignoreRule{}, false
	}
	r.re = re
	return r, true
}

// globToRegexp translates gitignore glob syntax (*, ?, [...], **) to a regexp.
func globToRegexp(glob string) string {
	var sb strings.Builder
	for i := 0; i < len(glob); i++ {
		c := glob[i]
		switch {
		case c == '*' && strings.HasPrefix(glob[i:], "**/"):
			sb.WriteString("(?:.*/)?")
			i += 2
		case c == '*' && glob[i:] == "**":
			sb.WriteString(".*")
			i++
		case c == '*':
			sb.WriteString("[^/]*")
		case c == '?':
			sb.WriteString("[^/]")
		case c == '[':
			end := strings.IndexByte(glob[i+1:], ']')
			if end < 0 {
				sb.WriteString(`\[`)
				continue
			}
			class := glob[i+1 : i+1+end]
			if strings.HasPrefix(class, "!") {
				class = "^" + class[1:]
			}
			sb.WriteString("[" + strings.ReplaceAll(class, `\`, `\\`) + "]")
			i += end + 1
		case c == '\\' && i+1 < len(glob):
			i++
			sb.WriteString(regexp.QuoteMeta(string(glob[i])))
		default:
			sb.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	return sb.String()
}
