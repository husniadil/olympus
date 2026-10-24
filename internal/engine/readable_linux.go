//go:build linux

package engine

import "syscall"

// The descriptor set's word size differs from Darwin's. It is isolated here so
// waitReadable itself stays portable.

func fdSet(set *syscall.FdSet, fd int) { set.Bits[fd/64] |= int64(1) << (uint(fd) % 64) }

func fdIsSet(set *syscall.FdSet, fd int) bool {
	return set.Bits[fd/64]&(int64(1)<<(uint(fd)%64)) != 0
}

func selectRead(nfd int, set *syscall.FdSet) error {
	_, err := syscall.Select(nfd, set, nil, nil, nil)
	return err
}
