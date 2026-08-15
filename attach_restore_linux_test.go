//go:build linux

package olympus_test

import "syscall"

const ioctlGetTermios = syscall.TCGETS
