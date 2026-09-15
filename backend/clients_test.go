package backend_test

import (
	"strings"
	"testing"

	"github.com/husniadil/olympus/backend"
)

// §13.5 A client tag a caller chooses is held to the shape the server holds
// it to: one to 128 bytes, and no control character. Anything else is input
// the caller could have validated, so it is USAGE before a server is asked.
func TestAClientTagIsOneTo128BytesWithNoControlCharacter(t *testing.T) {
	t.Parallel()
	for _, tag := range []string{
		"a",
		"browser-1",
		"olympus-client-0123456789abcdef",
		strings.Repeat("x", 128),
		"ünïcødé tag with spaces",
		strings.Repeat("é", 64), // 128 bytes
	} {
		if err := backend.CheckClientTag(tag); err != nil {
			t.Errorf("CheckClientTag(%q) = %v, want nil", tag, err)
		}
	}
	for _, tag := range []string{
		"",
		strings.Repeat("x", 129),
		strings.Repeat("é", 64) + "x", // 129 bytes
		"tab\there",
		"line\nbreak",
		"escape\x1b[0m",
		"delete\x7f",
		"c1control",
		"invalid \xff utf-8",
	} {
		if err := backend.CheckClientTag(tag); backend.CodeOf(err) != backend.CodeUsage {
			t.Errorf("CheckClientTag(%q) is %q (%v), want %q", tag, backend.CodeOf(err), err, backend.CodeUsage)
		}
	}
}
