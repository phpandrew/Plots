//go:build windows

package main

import "syscall"

func syscallSyscallN(p uintptr, args ...uintptr) (uintptr, uintptr, syscall.Errno) {
	return syscall.SyscallN(p, args...)
}
