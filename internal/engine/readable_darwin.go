//go:build darwin

package engine

import "syscall"

// Darwin's descriptor set is made of 32-bit words, and its Select returns no
// count. Both are isolated here so waitReadable itself stays portable.

func fdSet(set *syscall.FdSet, fd int) { set.Bits[fd/32] |= int32(1) << (uint(fd) % 32) }

func fdIsSet(set *syscall.FdSet, fd int) bool {
	return set.Bits[fd/32]&(int32(1)<<(uint(fd)%32)) != 0
}

func selectRead(nfd int, set *syscall.FdSet) error {
	return syscall.Select(nfd, set, nil, nil, nil)
}
