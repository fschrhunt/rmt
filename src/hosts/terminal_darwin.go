package hosts

import "golang.org/x/sys/unix"

const terminalGet = unix.TIOCGETA
const terminalReset = unix.TIOCSETAF

// disableTerminalSuspend avoids stopping the shell's entire foreground group without job control.
func disableTerminalSuspend(settings *unix.Termios) {
	const disabled = 0xff // Darwin's _POSIX_VDISABLE
	settings.Cc[unix.VSUSP] = disabled
	settings.Cc[unix.VDSUSP] = disabled
}
