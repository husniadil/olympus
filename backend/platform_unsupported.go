//go:build !darwin && !linux

// Olympus supports macOS and Linux only.
//
// The backends it drives are Unix programs and the attach path is termios and
// flock all the way down, so there is no partial build worth having here. This
// file makes that say itself at compile time: without it the failure is a wall
// of `undefined: syscall.Flock` from several packages at once, which reads like
// a broken checkout rather than an answer.
//
// It lives in the deepest package on purpose. Everything imports backend, so
// this is the first thing that fails and therefore the first thing reported.

package backend

const _ = OLYMPUS_SUPPORTS_ONLY_MACOS_AND_LINUX
