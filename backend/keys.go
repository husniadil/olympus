package backend

import "strconv"

// The key vocabulary is open, not a fixed list.
//
// A closed list is the obvious design and is wrong: driving a full-screen
// program means pressing whatever IT binds, and editors and TUIs use most of
// the control range and the function keys. Enumerating a handful of control
// letters means a caller simply cannot press Ctrl-X, which is how you leave
// nano — and the failure is a usage error naming a key that plainly exists.
//
// So five shapes are legal, and every backend translates all five:
//
//   - a named key, from the constants above (enter, escape, page-up, delete,
//     s-tab, c-up, s-up, m-up, m-enter, …)
//   - c-<letter> for any ASCII letter: c-a … c-z
//   - m-<letter> for any ASCII letter with alt held: m-a … m-z
//   - m-<symbol> for any other printable ASCII character with alt held, spelled
//     as the character itself: m-0, m-/, m-;, m-\ …
//   - f<n> for function keys: f1 … f12
//
// Anything else is CodeUsage, which keeps the conformance rule that an unknown
// key is the caller's to fix.

// ControlLetter reports the letter of a c-<letter> key, or 0 if the key is not
// one. The letter is returned lowercase.
func ControlLetter(k Key) byte {
	if len(k) != 3 || k[0] != 'c' || k[1] != '-' {
		return 0
	}
	letter := k[2]
	if letter >= 'A' && letter <= 'Z' {
		letter += 'a' - 'A'
	}
	if letter < 'a' || letter > 'z' {
		return 0
	}
	return letter
}

// MetaLetter reports the letter of an m-<letter> key, or 0 if the key is not
// one. The letter is returned lowercase.
//
// Alt is not a bit on the byte the way control is: a terminal sends ESC and
// then the letter, which is how readline's M-b and M-f arrive.
func MetaLetter(k Key) byte {
	if len(k) != 3 || k[0] != 'm' || k[1] != '-' {
		return 0
	}
	letter := k[2]
	if letter >= 'A' && letter <= 'Z' {
		letter += 'a' - 'A'
	}
	if letter < 'a' || letter > 'z' {
		return 0
	}
	return letter
}

// MetaSymbol reports the character of an m-<symbol> key, or 0 if the key is not
// one: a digit or punctuation mark, any printable ASCII character that is
// neither a letter nor a space.
//
// The character is spelled as itself, however hostile it is to a shell, since
// no shell stands between a caller and the key. Space, the control range, DEL
// and anything past ASCII are not symbols: a key bar sends alt with a single
// printable byte, and those are either named keys already or not one keypress.
func MetaSymbol(k Key) byte {
	if len(k) != 3 || k[0] != 'm' || k[1] != '-' {
		return 0
	}
	c := k[2]
	if c < '!' || c > '~' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
		return 0
	}
	return c
}

// FunctionNumber reports the n of an f<n> key, or 0 if the key is not one.
//
// Capped at 12: terminals encode higher function keys inconsistently, and
// accepting one Olympus cannot faithfully deliver would be worse than saying it
// is unknown.
func FunctionNumber(k Key) int {
	if len(k) < 2 || k[0] != 'f' {
		return 0
	}
	n, err := strconv.Atoi(string(k[1:]))
	if err != nil || n < 1 || n > 12 {
		return 0
	}
	return n
}
