package engine

import "strings"

// PastePayload is the text a paste hands its backend: the caller's text with
// every bracketed-paste marker taken out (behavior §4.6). An end marker inside
// the text would close the bracket early, and a consumer that honours the
// bracket would then run each line after it, though a paste never submits.
// A start marker is removed with it so the text cannot open a bracket of its
// own either.
func PastePayload(text string) string {
	// Repeated until nothing changes: taking one marker out can join the
	// halves of another around it.
	for {
		cleaned := strings.ReplaceAll(strings.ReplaceAll(text, "\x1b[201~", ""), "\x1b[200~", "")
		if cleaned == text {
			return text
		}
		text = cleaned
	}
}
