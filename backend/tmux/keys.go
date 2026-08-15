package tmux

import "github.com/husniadil/olympus/backend"

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
