package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"syscall"
)

var (
	YTCmd      string
	FFprobeCmd string
)

func RunCommand(ctx context.Context, command string, flags ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, command, flags...)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			fmt.Printf("runcommand exit error, stderr '%v'\n", string(ee.Stderr))
		}
	}
	return out, err
}

func RunCommandCh(ctx context.Context, command string, flags ...string) (chan string, chan error) {
	r, w := io.Pipe()
	cmd := exec.CommandContext(ctx, command, flags...)
	// set process group so that children can be killed
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stdout = w
	cmd.Stderr = w
	stdout := bufio.NewReader(r)
	outCh := make(chan string, 0)
	errCh := make(chan error, 0)
	go func() {
		for {
			line, err := stdout.ReadString('\n')
			outCh <- line
			if err != nil {
				if !errors.Is(err, io.EOF) {
					errCh <- err
				}
				return
			}
		}
	}()
	go func() {
		defer close(outCh)
		defer close(errCh)
		err := cmd.Run()
		if err != nil {
			errCh <- err
		}
		// kill any orphaned children upon completion, ignore kill error
		syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	}()

	return outCh, errCh
}
