//go:build !windows

// Package win provides console helpers; on non-Windows platforms they are no-ops.
package win

import "os"

// EnableANSI reports whether ANSI escapes are supported.
func EnableANSI() bool { return true }

// SetUTF8Output is a no-op outside Windows.
func SetUTF8Output() {}

// LaunchedFromShell always reports true outside Windows.
func LaunchedFromShell() bool { return true }

// IsTerminal reports whether stdout looks interactive.
func IsTerminal() bool {
	st, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return st.Mode()&os.ModeCharDevice != 0
}

// Pause waits for Enter.
func Pause() {
	os.Stdout.WriteString("\nPress Enter to close...")
	var buf [1]byte
	os.Stdin.Read(buf[:])
}
