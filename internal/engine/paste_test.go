package engine_test

import (
	"testing"

	"github.com/husniadil/olympus/internal/engine"
)

func TestAPasteCannotCloseItsOwnBracket(t *testing.T) {
	cases := map[string]string{
		"echo first\x1b[201~\necho second\n": "echo first\necho second\n",
		"\x1b[200~inner\x1b[201~":            "inner",
		// Removing one marker must not join the halves of another.
		"\x1b[20\x1b[201~1~": "",
		"plain\ntext":        "plain\ntext",
	}
	for in, want := range cases {
		if got := engine.PastePayload(in); got != want {
			t.Errorf("PastePayload(%q) = %q, want %q", in, got, want)
		}
	}
}
