package tmux

import (
	"strconv"

	"github.com/husniadil/olympus/backend"
)

// keyName translates a key into tmux's spelling, or reports it as unknown.
//
// The named table below covers the keys whose tmux spelling is not derivable.
// The control range and the function keys are derived instead, so the whole of
// each is available rather than whichever few someone thought to list.
func keyName(k backend.Key) (string, bool) {
	if name, ok := keyNames[k]; ok {
		return name, true
	}
	if letter := backend.ControlLetter(k); letter != 0 {
		// tmux writes these as C-x, and is case-insensitive about the letter.
		return "C-" + string(letter), true
	}
	if n := backend.FunctionNumber(k); n != 0 {
		return "F" + strconv.Itoa(n), true
	}
	return "", false
}

// keyNames translates Olympus's neutral key vocabulary into tmux's spelling.
// A key absent from this table is a usage error, raised before tmux is invoked.
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
	backend.KeyCtrlA:     "C-a",
	backend.KeyCtrlC:     "C-c",
	backend.KeyCtrlD:     "C-d",
	backend.KeyCtrlE:     "C-e",
	backend.KeyCtrlL:     "C-l",
	backend.KeyCtrlU:     "C-u",
	backend.KeyCtrlZ:     "C-z",
}
