package terminal

import (
	"os"

	"golang.org/x/sys/windows"
)

const (
	EnterKey = '\r'
)

type State struct {
	stdin  uint32
	stdout uint32
}

func ConfigureTerminal() (*State, error) {
	stdinHandle := windows.Handle(os.Stdin.Fd())
	stdoutHandle := windows.Handle(os.Stdout.Fd())

	var stdinState, stdoutState uint32
	err := windows.GetConsoleMode(stdinHandle, &stdinState)
	if err != nil {
		return nil, err
	}
	err = windows.GetConsoleMode(stdoutHandle, &stdoutState)
	if err != nil {
		return nil, err
	}
	oldStdinState := stdinState
	oldStdoutState := stdoutState

	stdinState &^= windows.ENABLE_ECHO_INPUT | windows.ENABLE_LINE_INPUT
	stdoutState |= windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING

	err = windows.SetConsoleMode(stdinHandle, stdinState)
	if err != nil {
		return nil, err
	}

	err = windows.SetConsoleMode(stdoutHandle, stdoutState)
	if err != nil {
		return nil, err
	}

	return &State{
		stdin:  oldStdinState,
		stdout: oldStdoutState,
	}, nil
}

func RestoreTerminal(oldState *State) error {
	stdinHandle := windows.Handle(os.Stdin.Fd())
	stdoutHandle := windows.Handle(os.Stdout.Fd())

	stdinErr := windows.SetConsoleMode(stdinHandle, oldState.stdin)
	stdoutErr := windows.SetConsoleMode(stdoutHandle, oldState.stdout)

	if stdinErr != nil {
		return stdinErr
	}
	if stdoutErr != nil {
		return stdoutErr
	}

	return nil
}
