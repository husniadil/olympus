package meja

import (
	"strconv"

	"github.com/husniadil/olympus/backend"
)

// keyName translates a key into meja's spelling, or reports it as unknown.
//
// meja uses tmux's spelling — C-a, Enter, Up — and the table below is
// deliberately its own copy rather than an import from the tmux backend. The
// two agree today by meja's choice, not by contract; sharing the table would
// make one backend's compatibility decision the other's dependency, and a
// divergence would then surface as a puzzling failure in the wrong package.
//
// They have already diverged once: meja spells forward delete "Delete", and
// takes tmux's "DC" as three literal characters (measured with `cat -v`).
func keyName(k backend.Key) (string, bool) {
	if name, ok := keyNames[k]; ok {
		return name, true
	}
	if letter := backend.ControlLetter(k); letter != 0 {
		return "C-" + string(letter), true
	}
	if letter := backend.MetaLetter(k); letter != 0 {
		return "M-" + string(letter), true
	}
	if c := backend.MetaSymbol(k); c != 0 {
		return "M-" + string(c), true
	}
	if n := backend.FunctionNumber(k); n != 0 {
		return "F" + strconv.Itoa(n), true
	}
	return "", false
}

var keyNames = map[backend.Key]string{
	backend.KeyEnter:      "Enter",
	backend.KeyEscape:     "Escape",
	backend.KeyTab:        "Tab",
	backend.KeyBackspace:  "BSpace",
	backend.KeySpace:      "Space",
	backend.KeyUp:         "Up",
	backend.KeyDown:       "Down",
	backend.KeyLeft:       "Left",
	backend.KeyRight:      "Right",
	backend.KeyHome:       "Home",
	backend.KeyEnd:        "End",
	backend.KeyPageUp:     "PageUp",
	backend.KeyPageDown:   "PageDown",
	backend.KeyDelete:     "Delete",
	backend.KeyCtrlUp:     "C-Up",
	backend.KeyCtrlDown:   "C-Down",
	backend.KeyCtrlRight:  "C-Right",
	backend.KeyCtrlLeft:   "C-Left",
	backend.KeyMetaEnter:  "M-Enter",
	backend.KeyShiftUp:    "S-Up",
	backend.KeyShiftDown:  "S-Down",
	backend.KeyShiftRight: "S-Right",
	backend.KeyShiftLeft:  "S-Left",
	backend.KeyMetaUp:     "M-Up",
	backend.KeyMetaDown:   "M-Down",
	backend.KeyMetaRight:  "M-Right",
	backend.KeyMetaLeft:   "M-Left",
}

// keyLiterals are the keys meja's send-keys has no name for, spelled as the
// bytes a terminal sends and written with `send-keys -l`, which meja passes to
// the pane unencoded.
//
// Back-tab is the one. meja parses "S-Tab" and then drops the shift: its
// legacy encoding of a modified Tab is a plain tab, so the pane receives ^I
// where ^[[Z was meant (measured with `cat -v` on meja 0.0.26), and "BTab" is
// typed as four letters. The literal path delivers ^[[Z.
var keyLiterals = map[backend.Key]string{
	backend.KeyShiftTab: "\x1b[Z",
}
