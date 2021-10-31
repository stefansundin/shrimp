//go:build !windows

package terminal

import (
	"os"

	"github.com/pkg/term/termios"
	"golang.org/x/sys/unix"
)

const (
	EnterKey = '\n'
)

func ConfigureTerminal() (*unix.Termios, *unix.Termios, error) {
	fd := os.Stdin.Fd()

	var state unix.Termios
	err := termios.Tcgetattr(fd, &state)
	if err != nil {
		return nil, nil, err
	}
	oldState := state

	// Configure terminal to send single characters to stdin
	// This is some black magic.. check the termios man page
	state.Lflag &^= unix.ECHO | unix.ECHONL | unix.ICANON
	err = termios.Tcsetattr(fd, termios.TCSANOW, &state)
	if err != nil {
		return nil, nil, err
	}

	return &oldState, nil, nil
}

func RestoreTerminal(stdinState, stdoutState *unix.Termios) error {
	fd := os.Stdin.Fd()
	return termios.Tcsetattr(fd, termios.TCSANOW, stdinState)
}
