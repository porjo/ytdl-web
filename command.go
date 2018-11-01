package main

import (
	"context"
	"io"
	"os/exec"
)

func RunCommandCh(ctx context.Context, w io.WriteCloser, command string, flags ...string) error {
	defer w.Close() // this will unblock the reader
	cmd := exec.CommandContext(ctx, command, flags...)
	cmd.Stdout = w
	//cmd.Stderr = os.Stderr // or set this to w as well
	cmd.Stderr = w
	return cmd.Run()
}
