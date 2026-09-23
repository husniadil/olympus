package cli_test

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/husniadil/olympus/internal/cli"
	"github.com/husniadil/olympus/internal/mcp"
)

// The documents that print commands somebody is meant to type. A verb or flag
// renamed in the command tree and not here leaves them telling a person or an
// agent to run something the CLI refuses: it reads perfectly and fails only
// when used, which nothing else catches.
var speaking = []string{
	"README.md",
	"CONTRIBUTING.md",
	"skills/olympus/SKILL.md",
	"docs/api.md",
	"docs/adding-a-backend.md",
	"docs/known-issues.md",
}

func repoFile(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

var (
	fence      = regexp.MustCompile("(?s)```[a-z]*\n(.*?)```")
	inlineCode = regexp.MustCompile("`(olympus [^`]+)`")
	optional   = regexp.MustCompile(`\[[^\]]*\]`)
	holder     = regexp.MustCompile(`<([^>]+)>`)
)

// documentedCommands returns every `olympus ...` line a document prints, in a
// code block or inline, each with where it came from.
func documentedCommands(doc string) []string {
	var lines []string
	for _, m := range fence.FindAllStringSubmatch(doc, -1) {
		for _, line := range strings.Split(m[1], "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "olympus ") {
				lines = append(lines, line)
			}
		}
	}
	for _, m := range inlineCode.FindAllStringSubmatch(doc, -1) {
		lines = append(lines, m[1])
	}
	return lines
}

// argv splits one documented line the way a shell would, far enough to name
// the verb and its flags. It stops at a comment, a pipe or a second command,
// and fills a <placeholder> with a word so a schema line is checked too.
func argv(line string) []string {
	line = optional.ReplaceAllString(line, " ")
	line = holder.ReplaceAllString(line, "x")
	var out []string
	var cur strings.Builder
	var quote rune
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	for _, r := range line {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
				out = append(out, cur.String())
				cur.Reset()
				continue
			}
			cur.WriteRune(r)
		case r == '\'' || r == '"':
			flush()
			quote = r
		case r == ' ' || r == '\t':
			flush()
		case r == '#' && cur.Len() == 0, r == '|', r == ';', r == '&':
			flush()
			return out[1:]
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	return out[1:]
}

func TestEveryDocumentedCommandParses(t *testing.T) {
	for _, rel := range speaking {
		for _, line := range documentedCommands(repoFile(t, rel)) {
			args := argv(line)
			if len(args) == 0 || strings.Contains(line, "$(") && len(args) < 2 {
				continue
			}
			root := cli.Root()
			cmd, rest, err := root.Find(args)
			if err != nil {
				t.Errorf("%s: %q names no verb: %v", rel, line, err)
				continue
			}
			if cmd == root && !strings.HasPrefix(args[0], "-") {
				t.Errorf("%s: %q names no verb", rel, line)
				continue
			}
			if slices.Contains(rest, "--help") || slices.Contains(rest, "-h") {
				continue
			}
			if err := cmd.ParseFlags(rest); err != nil {
				t.Errorf("%s: %q does not parse: %v", rel, line, err)
			}
		}
	}
}

// api §1 is the one table that names every operation at every door. Held here
// against the command tree and the served tools, so neither can move without
// the table moving with it.
func TestTheVocabularyTableMatchesTheDoors(t *testing.T) {
	doc := repoFile(t, "docs/api.md")
	start := strings.Index(doc, "| Operation | CLI | MCP tool | Go |")
	if start < 0 {
		t.Fatal("api §1's table header is gone")
	}
	var verbs, tools []string
	for _, row := range strings.Split(doc[start:], "\n")[2:] {
		if !strings.HasPrefix(row, "|") {
			break
		}
		cells := strings.Split(row, "|")
		for _, m := range regexp.MustCompile("`([^`]+)`").FindAllStringSubmatch(cells[2], -1) {
			verbs = append(verbs, strings.Join(strings.Fields(m[1]), " "))
		}
		for _, m := range regexp.MustCompile("`([a-z_]+)`").FindAllStringSubmatch(cells[3], -1) {
			if !slices.Contains(tools, m[1]) {
				tools = append(tools, m[1])
			}
		}
	}

	root := cli.Root()
	documented := map[string]bool{}
	for _, v := range verbs {
		documented[v] = true
		if cmd, _, err := root.Find(strings.Fields(v)); err != nil || cmd == root {
			t.Errorf("api §1 names CLI verb %q, which the command tree does not have", v)
		}
	}
	var walk func(*cobra.Command, string)
	walk = func(c *cobra.Command, prefix string) {
		for _, sub := range c.Commands() {
			name := strings.TrimSpace(prefix + " " + sub.Name())
			if sub.Hidden || name == "help" || name == "completion" {
				continue
			}
			if !documented[name] && !sub.HasSubCommands() {
				t.Errorf("verb %q is missing from api §1", name)
			}
			walk(sub, name)
		}
	}
	walk(root, "")

	slices.Sort(tools)
	served := slices.Clone(mcp.ToolNames)
	slices.Sort(served)
	if !slices.Equal(tools, served) {
		t.Errorf("api §1's MCP column names %v\nToolNames serves %v", tools, served)
	}
}
