package command

import (
	"bufio"
	"context"
	"errors"
	"io"
	"log/slog"
	"os/exec"
	"sync"
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
			slog.Debug("runcommand exit error", "stderr", string(ee.Stderr))
		}
	}
	return out, err
}

// Credit to: https://blog.kowalczyk.info/article/wOYk/advanced-command-execution-in-go-with-osexec.html
func RunCommandCh(ctx context.Context, command string, flags ...string) (chan string, chan error, error) {
	cmd := exec.CommandContext(ctx, command, flags...)
	// set process group so that children can be killed
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	outCh := make(chan string)
	errCh := make(chan error)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	stdoutBuf := bufio.NewReader(stdout)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, nil, err
	}
	stderrBuf := bufio.NewReader(stderr)
	go func() {
		defer close(outCh)
		defer close(errCh)
		slog.Debug("command start", "command", command)
		err := cmd.Start()
		if err != nil {
			nonblockingChSend(errCh, err)
		}

		wg := sync.WaitGroup{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				line, err := stdoutBuf.ReadString('\n')
				if err != nil {
					if !errors.Is(err, io.EOF) {
						nonblockingChSend(errCh, err)
					}
					return
				}
				nonblockingChSend(outCh, line)
			}
		}()

		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				line, err := stderrBuf.ReadString('\n')
				if err != nil {
					if !errors.Is(err, io.EOF) {
						nonblockingChSend(errCh, err)
					}
					break
				}
				nonblockingChSend(outCh, line)
			}
		}()

		slog.Debug("wait for stdout/stderr readers to end", "command", command)
		wg.Wait()

		slog.Debug("command wait", "command", command)
		err = cmd.Wait()
		if err != nil {
			nonblockingChSend(errCh, err)
		}
		slog.Debug("command wait, done", "command", command)
		// kill any orphaned children upon completion, ignore kill error
		syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	}()

	return outCh, errCh, nil
}

func nonblockingChSend[T any](ch chan T, msg T) {
	select {
	case ch <- msg:
	default:
		slog.Warn("channel was blocked, message discarded", "msg", msg)
	}
}
