// Command gendoc writes the CLI's manual pages and shell completions.
//
// Generated from the command tree itself rather than maintained separately, so
// a new verb or flag cannot ship with documentation that silently still
// describes the old surface. It is a tool, not part of the shipped binary.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra/doc"

	"github.com/husniadil/olympus/internal/cli"
)

func main() {
	out := "dist/doc"
	if len(os.Args) > 1 {
		out = os.Args[1]
	}

	root := cli.Root()
	root.DisableAutoGenTag = true

	man := filepath.Join(out, "man")
	completions := filepath.Join(out, "completions")
	for _, dir := range []string{man, completions} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			fail(err)
		}
	}

	if err := doc.GenManTree(root, &doc.GenManHeader{
		Title:   "OLYMPUS",
		Section: "1",
		Source:  "Olympus",
		Manual:  "Olympus Manual",
	}, man); err != nil {
		fail(err)
	}

	for shell, gen := range map[string]func(string) error{
		"olympus.bash": root.GenBashCompletionFile,
		"olympus.zsh":  root.GenZshCompletionFile,
		"olympus.fish": func(path string) error { return root.GenFishCompletionFile(path, true) },
	} {
		if err := gen(filepath.Join(completions, shell)); err != nil {
			fail(err)
		}
	}

	fmt.Printf("wrote manual pages to %s and completions to %s\n", man, completions)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "gendoc:", err)
	os.Exit(1)
}
