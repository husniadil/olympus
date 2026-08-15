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
func keyName(k backend.Key) (string, bool) {
	if name, ok := keyNames[k]; ok {
		return name, true
	}
	if letter := backend.ControlLetter(k); letter != 0 {
		return "C-" + string(letter), true
	}
	if n := backend.FunctionNumber(k); n != 0 {
		return "F" + strconv.Itoa(n), true
	}
	return "", false
}

var keyNames = map[backend.Key]string{
	backend.KeyEnter:     "Enter",
	backend.KeyEscape:    "Escape",
	backend.KeyTab:       "Tab",
	backend.KeyBackspace: "BSpace",
	backend.KeySpace:     "Space",
	backend.KeyUp:        "Up",
	backend.KeyDown:      "Down",
	backend.KeyLeft:      "Left",
	backend.KeyRight:     "Right",
	backend.KeyHome:      "Home",
	backend.KeyEnd:       "End",
	backend.KeyPageUp:    "PageUp",
	backend.KeyPageDown:  "PageDown",
}
