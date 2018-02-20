package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
)

func RunCommandCh(ctx context.Context, w io.WriteCloser, command string, flags ...string) error {
	defer w.Close() // this will unblock the reader
	cmd := exec.CommandContext(ctx, command, flags...)
	cmd.Stdout = w
	cmd.Stderr = os.Stderr // or set this to w as well
	return cmd.Run()
}

func scanCR(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}

	if i := bytes.IndexByte(data, '\n'); i >= 0 {
		return i + 1, dropCR(data[0:i]), nil
	}
	if i := bytes.IndexByte(data, '\r'); i >= 0 {
		return i + 1, data[0:i], nil
	}

	// If we're at EOF, we have a final, non-terminated line. Return it.
	if atEOF {
		return len(data), dropCR(data), nil
	}

	// Request more data.
	return 0, nil, nil
}

// dropCR drops a terminal \r from the data.
func dropCR(data []byte) []byte {
	if len(data) > 0 && data[len(data)-1] == '\r' {

		return data[0 : len(data)-1]

	}
	return data
}
