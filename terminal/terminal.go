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

type State struct {
	stdin *unix.Termios
}

func ConfigureTerminal() (*State, error) {
	fd := os.Stdin.Fd()

	var state unix.Termios
	err := termios.Tcgetattr(fd, &state)
	if err != nil {
		return nil, err
	}
	oldState := state

	// Configure terminal to send single characters to stdin
	// This is some black magic.. check the termios man page
	state.Lflag &^= unix.ECHO | unix.ECHONL | unix.ICANON
	err = termios.Tcsetattr(fd, termios.TCSANOW, &state)
	if err != nil {
		return nil, err
	}

	return &State{stdin: &oldState}, nil
}

func RestoreTerminal(state *State) error {
	fd := os.Stdin.Fd()
	return termios.Tcsetattr(fd, termios.TCSANOW, state.stdin)
}
