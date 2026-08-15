//go:build darwin || linux

package engine

import (
	"syscall"
	"unsafe"
)

// Terminal-attribute handling, written against the standard library rather than
// pulling in a fourth dependency. The dependency budget is three libraries and
// is a deliberate constraint, not an accident, and this is roughly forty lines.

func getTermios(fd uintptr) (syscall.Termios, error) {
	var state syscall.Termios
	if _, _, errno := syscall.Syscall6(syscall.SYS_IOCTL, fd, uintptr(ioctlGetTermios),
		uintptr(unsafe.Pointer(&state)), 0, 0, 0); errno != 0 {
		return state, errno
	}
	return state, nil
}

func setTermios(fd uintptr, state syscall.Termios) error {
	if _, _, errno := syscall.Syscall6(syscall.SYS_IOCTL, fd, uintptr(ioctlSetTermios),
		uintptr(unsafe.Pointer(&state)), 0, 0, 0); errno != 0 {
		return errno
	}
	return nil
}

// rawMode returns the attributes for a terminal that forwards keystrokes
// instead of interpreting them.
//
// Clearing ISIG is the point of the exercise. Without raw mode the OUTER line
// discipline interprets keys itself, so Ctrl+C raises SIGINT against the
// Olympus process — a spurious detach — instead of delivering 0x03 to the inner
// shell. With ISIG cleared, Ctrl+C, Ctrl+Z and Ctrl+\ all forward inward.
// Detaching is the inner backend's job, not the outer terminal's (behavior
// §8.2).
func rawMode(state syscall.Termios) syscall.Termios {
	raw := state
	raw.Iflag &^= syscall.IGNBRK | syscall.BRKINT | syscall.PARMRK | syscall.ISTRIP |
		syscall.INLCR | syscall.IGNCR | syscall.ICRNL | syscall.IXON
	raw.Oflag &^= syscall.OPOST
	raw.Lflag &^= syscall.ECHO | syscall.ECHONL | syscall.ICANON | syscall.ISIG | syscall.IEXTEN
	raw.Cflag &^= syscall.CSIZE | syscall.PARENB
	raw.Cflag |= syscall.CS8
	raw.Cc[syscall.VMIN] = 1
	raw.Cc[syscall.VTIME] = 0
	return raw
}
