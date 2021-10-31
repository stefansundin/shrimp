package terminal

import (
	"os"

	"golang.org/x/sys/windows"
)

const (
	EnterKey = '\r'
)

func ConfigureTerminal() (uint32, uint32, error) {
	stdinHandle := windows.Handle(os.Stdin.Fd())
	stdoutHandle := windows.Handle(os.Stdout.Fd())

	var stdinState, stdoutState uint32
	err := windows.GetConsoleMode(stdinHandle, &stdinState)
	if err != nil {
		return 0, 0, err
	}
	err = windows.GetConsoleMode(stdoutHandle, &stdoutState)
	if err != nil {
		return 0, 0, err
	}
	oldStdinState := stdinState
	oldStdoutState := stdoutState

	stdinState &^= windows.ENABLE_ECHO_INPUT | windows.ENABLE_LINE_INPUT
	stdoutState |= windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING

	err = windows.SetConsoleMode(stdinHandle, stdinState)
	if err != nil {
		return 0, 0, err
	}

	err = windows.SetConsoleMode(stdoutHandle, stdoutState)
	if err != nil {
		return 0, 0, err
	}

	return oldStdinState, oldStdoutState, nil
}

func RestoreTerminal(stdinState, stdoutState uint32) error {
	stdinHandle := windows.Handle(os.Stdin.Fd())
	stdoutHandle := windows.Handle(os.Stdout.Fd())

	stdinErr := windows.SetConsoleMode(stdinHandle, stdinState)
	stdoutErr := windows.SetConsoleMode(stdoutHandle, stdoutState)

	if stdinErr != nil {
		return stdinErr
	}
	if stdoutErr != nil {
		return stdoutErr
	}

	return nil
}
