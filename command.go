package main

import (
	"context"
	"fmt"
	//	"io"
	"bufio"
	"log"
	//	"os"
	"os/exec"
	"strings"
)

// RunCommandCh runs an arbitrary command and streams output to a channnel.
// Based on: https://bountify.co/golang-parse-stdout
func RunCommandCh(ctx context.Context, stdoutCh chan<- string, command string, flags ...string) error {
	fmt.Printf("%v %v\n", command, flags)
	cmd := exec.Command(command, flags...)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("RunCommand: cmd.StdoutPipe(): %v", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("RunCommand: cmd.Start(): %v", err)
	}

	scannerStdout := bufio.NewScanner(stdout)
	go func() {
		for {
			select {
			case <-ctx.Done():
				log.Printf("RunCommand: process time expired, killing cmd\n")
				err := cmd.Process.Kill()
				if err != nil {
					log.Panic(err)
				}
				return
			default:
			}
			if scannerStdout.Scan() {
				text := scannerStdout.Text()
				if strings.TrimSpace(text) != "" {
					stdoutCh <- text
				}
			} else {
				return
			}
		}
	}()

	if err := cmd.Wait(); err != nil {
		return err
	}
	close(stdoutCh)
	return nil
}
