package main

import (
	"context"
	"io"
	"os/exec"
	"syscall"
)

func RunCommandCh(ctx context.Context, w io.WriteCloser, command string, flags ...string) error {
	defer w.Close() // this will unblock the reader
	cmd := exec.CommandContext(ctx, command, flags...)
	// set process group so that children can be killed
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stdout = w
	//cmd.Stderr = os.Stderr // or set this to w as well
	cmd.Stderr = w
	err := cmd.Run()
	// kill process group including children
	syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	return err
}
