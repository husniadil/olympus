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
// So three shapes are legal, and every backend translates all three:
//
//   - a named key, from the constants above (enter, escape, page-up, …)
//   - c-<letter> for any ASCII letter: c-a … c-z
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
