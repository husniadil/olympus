//go:build darwin

package engine

import "syscall"

// Darwin spells the terminal-attribute ioctls differently from Linux. They are
// isolated here so the raw-mode logic itself stays portable.
const (
	ioctlGetTermios = syscall.TIOCGETA
	ioctlSetTermios = syscall.TIOCSETA
)
