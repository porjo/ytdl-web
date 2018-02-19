package main

import (
	"context"
	"fmt"
	"io"
	"log"
	//	"os"
	"bufio"
	"os/exec"
	"strings"
	"sync"
)

// RunCommandCh runs an arbitrary command and streams output to a channnel.
// Based on: https://bountify.co/golang-parse-stdout
func RunCommandCh(ctx context.Context, stdoutCh chan<- string, cutset string, command string, flags ...string) error {
	cmd := exec.Command(command, flags...)

	var wg sync.WaitGroup

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("RunCommand: cmd.StdoutPipe(): %v", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("RunCommand: cmd.StderrPipe(): %v", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("RunCommand: cmd.Start(): %v", err)
	}

	outch := make(chan string, 10)

	scannerStdout := bufio.NewScanner(stdout)
	scannerStdout.Split(ScanLinesR)
	wg.Add(1)
	go func() {
		for scannerStdout.Scan() {
			text := scannerStdout.Text()
			if strings.TrimSpace(text) != "" {
				outch <- text
			}
		}
		wg.Done()
	}()
	scannerStderr := bufio.NewScanner(stderr)
	scannerStderr.Split(ScanLinesR)
	wg.Add(1)
	go func() {
		for scannerStderr.Scan() {
			text := scannerStderr.Text()
			if strings.TrimSpace(text) != "" {
				outch <- text
			}
		}
		wg.Done()
	}()

	go func() {
		wg.Wait()
		close(outch)
	}()

	/*
		output, err := cmd.StdoutPipe()
		if err != nil {
			return fmt.Errorf("RunCommand: cmd.StdoutPipe(): %v", err)
		}
		stdErr, err := cmd.StderrPipe()
		if err != nil {
			return fmt.Errorf("RunCommand: cmd.StderrPipe(): %v", err)
		}

		/*
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
	*/

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("RunCommand: cmd.Start(): %v", err)
	}

	for {
		select {
		case <-ctx.Done():
			log.Printf("RunCommand: process time expired, killing cmd\n")
			err := cmd.Process.Kill()
			if err != nil {
				log.Panic(err)
			}
		default:
		}

		buf := make([]byte, 1024)
		n, err := output.Read(buf)
		if err != nil {
			if err == io.EOF {
				log.Printf("RunCommand: output EOF\n")
				break
			}
			return err
		}
		text := strings.Trim(string(buf[:n]), " ")

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

	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("RunCommand: cmd.Wait() err: %s", err)
	}
	log.Printf("RunCommand: end\n")
	close(stdoutCh)
	return nil
}
