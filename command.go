package main

import (
	"fmt"
	"io"
	"log"
	"os/exec"
	"strings"
)

// RunCommandCh runs an arbitrary command and streams output to a channnel.
// Credit to https://bountify.co/golang-parse-stdout
func RunCommandCh(stdoutCh chan<- string, cutset string, command string, flags ...string) error {
	cmd := exec.Command(command, flags...)

	output, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("RunCommand: cmd.StdoutPipe(): %v", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("RunCommand: cmd.Start(): %v", err)
	}

	go func() {
		defer close(stdoutCh)
		for {
			buf := make([]byte, 1024)
			n, err := output.Read(buf)
			if err != nil {
				if err != io.EOF {
					log.Fatal(err)
				}
				if n == 0 {
					break
				}
			}
			text := strings.TrimSpace(string(buf[:n]))
			for {
				// Take the index of any of the given cutset
				n := strings.IndexAny(text, cutset)
				if n == -1 {
					// If not found, but still have data, send it
					if len(text) > 0 {
						stdoutCh <- text
					}
					break
				}
				// Send data up to the found cutset
				stdoutCh <- text[:n]
				// If cutset is last element, stop there.
				if n == len(text) {
					break
				}
				// Shift the text and start again.
				text = text[n+1:]
			}
		}
	}()

	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("RunCommand: cmd.Wait(): %v", err)
	}
	return nil
}
