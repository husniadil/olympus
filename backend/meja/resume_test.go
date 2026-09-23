package meja

import (
	"errors"
	"testing"
)

// §2.10 A multi-step injection retried after a refusal resumes at the refused
// step. Rerunning it whole would deliver the steps that already landed a second
// time: text typed before the terminator was refused would be typed twice.
func TestARetriedInjectionResumesAtTheRefusedStep(t *testing.T) {
	refused := errors.New("command requires an attached client")
	var text, enter int
	op := resumable(
		func() error { text++; return nil },
		func() error {
			enter++
			if enter == 1 {
				return refused
			}
			return nil
		},
	)
	if err := op(); !errors.Is(err, refused) {
		t.Fatalf("the first attempt returned %v, want the refusal", err)
	}
	if err := op(); err != nil {
		t.Fatalf("the retry returned %v", err)
	}
	if text != 1 || enter != 2 {
		t.Errorf("the text ran %d times and the terminator %d; want 1 and 2", text, enter)
	}
	if err := op(); err != nil || text != 1 || enter != 2 {
		t.Errorf("a finished injection ran again (err %v, text %d, enter %d)", err, text, enter)
	}
}
