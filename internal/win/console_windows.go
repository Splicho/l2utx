//go:build windows

// Package win provides the small amount of Windows console glue the CLI needs.
package win

import (
	"os"
	"syscall"
	"unsafe"
)

var (
	kernel32                       = syscall.NewLazyDLL("kernel32.dll")
	procGetConsoleMode             = kernel32.NewProc("GetConsoleMode")
	procSetConsoleMode             = kernel32.NewProc("SetConsoleMode")
	procGetConsoleProcessList      = kernel32.NewProc("GetConsoleProcessList")
	procSetConsoleOutputCP         = kernel32.NewProc("SetConsoleOutputCP")
	procSetConsoleCtrlHandler      = kernel32.NewProc("SetConsoleCtrlHandler")
)

const enableVirtualTerminalProcessing = 0x0004

// EnableANSI turns on VT100 escape sequence handling for the current console.
func EnableANSI() bool {
	h, err := syscall.GetStdHandle(syscall.STD_OUTPUT_HANDLE)
	if err != nil {
		return false
	}
	var mode uint32
	r, _, _ := procGetConsoleMode.Call(uintptr(h), uintptr(unsafe.Pointer(&mode)))
	if r == 0 {
		return false
	}
	if mode&enableVirtualTerminalProcessing != 0 {
		return true
	}
	mode |= enableVirtualTerminalProcessing
	r, _, _ = procSetConsoleMode.Call(uintptr(h), uintptr(mode))
	return r != 0
}

// SetUTF8Output switches the console output code page to UTF-8 so box drawing
// and arrows render correctly.
func SetUTF8Output() {
	procSetConsoleOutputCP.Call(65001)
}

// LaunchedFromShell reports true when the process shares its console with a
// parent shell (i.e. it was started from cmd/PowerShell) rather than being
// spawned by Explorer for a drag & drop or double click. In the latter case the
// window disappears the moment the program exits, so the CLI pauses.
func LaunchedFromShell() bool {
	var list [8]uint32
	n, _, _ := procGetConsoleProcessList.Call(uintptr(unsafe.Pointer(&list[0])), uintptr(len(list)))
	// n == 0 -> no console at all (redirected output); treat as shell-like.
	return n > 1
}

// IsTerminal reports whether stdout is an interactive console.
func IsTerminal() bool {
	h, err := syscall.GetStdHandle(syscall.STD_OUTPUT_HANDLE)
	if err != nil {
		return false
	}
	var mode uint32
	r, _, _ := procGetConsoleMode.Call(uintptr(h), uintptr(unsafe.Pointer(&mode)))
	return r != 0
}

// Pause waits for the user to press Enter.
func Pause() {
	os.Stdout.WriteString("\nPress Enter to close this window...")
	var buf [1]byte
	os.Stdin.Read(buf[:])
}
