package engine

import "syscall"

// fdSetSize is FD_SETSIZE on both platforms Olympus runs on: select cannot
// watch a descriptor at or above it.
const fdSetSize = 1024

// waitReadable blocks until in has something to read or stop does, its
// writer closed. It reports whether in may be read, and whether it could
// watch at all: a descriptor select cannot take is not watched, and the
// caller reads as it would without.
//
// This is what lets an attach stop reading its caller's input when it ends. A
// read blocked on a terminal cannot be woken, so one left waiting after the
// attach returned took the caller's next input (forwardInput).
func waitReadable(in, stop int) (readable, watched bool) {
	if in < 0 || stop < 0 || in >= fdSetSize || stop >= fdSetSize {
		return false, false
	}
	for {
		var set syscall.FdSet
		fdSet(&set, in)
		fdSet(&set, stop)
		err := selectRead(max(in, stop)+1, &set)
		if err == syscall.EINTR {
			continue
		}
		if err != nil {
			return false, false
		}
		if fdIsSet(&set, stop) {
			return false, true
		}
		return fdIsSet(&set, in), true
	}
}
