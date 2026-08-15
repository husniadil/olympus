//go:build darwin

package olympus_test

import "syscall"

// Darwin spells the terminal-attribute ioctl differently from Linux, exactly as
// internal/engine does. Isolated here for the same reason: the test that reads a
// terminal's state stays portable, and only the constant moves.
const ioctlGetTermios = syscall.TIOCGETA
