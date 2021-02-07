package main

import (
	"github.com/pkg/term/termios"
	"golang.org/x/sys/unix"
)

func configureTerminal(fd uintptr) (*unix.Termios, error) {
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

	return &oldState, nil
}

func restoreTerminal(fd uintptr, state *unix.Termios) error {
	return termios.Tcsetattr(fd, termios.TCSANOW, state)
}
