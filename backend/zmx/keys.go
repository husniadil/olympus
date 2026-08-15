package zmx

import "github.com/husniadil/olympus/backend"

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
