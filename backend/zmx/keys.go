package zmx

import (
	"strconv"

	"github.com/husniadil/olympus/backend"
)

// keySequence translates a key into the bytes a terminal sends for it.
//
// The control range is derived rather than enumerated: a control key is the
// letter with the top three bits cleared, which is the whole of C-a through
// C-z in one line. Listing a handful by hand is how a caller ends up unable to
// press Ctrl-X.
func keySequence(k backend.Key) (string, bool) {
	if seq, ok := keySequences[k]; ok {
		return seq, true
	}
	if letter := backend.ControlLetter(k); letter != 0 {
		return string([]byte{letter - 'a' + 1}), true
	}
	if n := backend.FunctionNumber(k); n != 0 {
		return functionSequence(n), true
	}
	return "", false
}

// functionSequence is the xterm encoding of a function key. F1-F4 use the SS3
// form and the rest a CSI form with a non-contiguous parameter, which is a
// quirk of the original DEC keyboards rather than anything derivable.
func functionSequence(n int) string {
	switch n {
	case 1:
		return "\x1bOP"
	case 2:
		return "\x1bOQ"
	case 3:
		return "\x1bOR"
	case 4:
		return "\x1bOS"
	}
	codes := map[int]int{5: 15, 6: 17, 7: 18, 8: 19, 9: 20, 10: 21, 11: 23, 12: 24}
	return "\x1b[" + strconv.Itoa(codes[n]) + "~"
}

// keySequences translates Olympus's neutral key vocabulary into the bytes a
// terminal would send for that key. zmx takes raw input, so the backend spells
// the keys itself rather than handing a name to the multiplexer.
//
// KeyCtrlC is here as the literal keypress a caller asked for, and is NOT how
// Interrupt is implemented: writing 0x03 into a zmx session generates no
// terminal SIGINT at all, so a foreground job that would happily die survives
// it indefinitely (behavior §2.8.1, cause 1).
var keySequences = map[backend.Key]string{
	backend.KeyEnter:     "\r",
	backend.KeyEscape:    "\x1b",
	backend.KeyTab:       "\t",
	backend.KeyBackspace: "\x7f",
	backend.KeySpace:     " ",
	backend.KeyUp:        "\x1b[A",
	backend.KeyDown:      "\x1b[B",
	backend.KeyRight:     "\x1b[C",
	backend.KeyLeft:      "\x1b[D",
	backend.KeyHome:      "\x1b[H",
	backend.KeyEnd:       "\x1b[F",
	backend.KeyPageUp:    "\x1b[5~",
	backend.KeyPageDown:  "\x1b[6~",
	backend.KeyCtrlA:     "\x01",
	backend.KeyCtrlC:     "\x03",
	backend.KeyCtrlD:     "\x04",
	backend.KeyCtrlE:     "\x05",
	backend.KeyCtrlL:     "\x0c",
	backend.KeyCtrlU:     "\x15",
	backend.KeyCtrlZ:     "\x1a",
}
