package hosts

import "golang.org/x/sys/unix"

const terminalGet = unix.TCGETS
const terminalReset = unix.TCSETSF

// disableTerminalSuspend avoids stopping the shell's entire foreground group without job control.
func disableTerminalSuspend(settings *unix.Termios) { settings.Cc[unix.VSUSP] = 0 }
