package gateway

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// process is a supervised child (tor or caddy).
type process struct {
	name string
	cmd  *exec.Cmd
	done chan struct{}
	err  error
}

// exitEvent is delivered when a child terminates.
type exitEvent struct {
	proc *process
}

// startProcess launches a child in its own process group (so a terminal
// Ctrl-C reaches only OnionForge, which then stops children in order) and
// forwards its output line by line to the logger. onLine, if set, observes
// every output line.
func startProcess(log *Logger, name string, env []string, exits chan<- exitEvent, onLine func(string), bin string, args ...string) (*process, error) {
	cmd := exec.Command(bin, args...)
	cmd.Env = append(os.Environ(), env...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw
	if err := cmd.Start(); err != nil {
		pw.Close()
		return nil, fmt.Errorf("starting %s: %w", name, err)
	}
	p := &process{name: name, cmd: cmd, done: make(chan struct{})}
	scanDone := make(chan struct{})
	go func() {
		defer close(scanDone)
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			line := sc.Text()
			log.Child(name, line)
			if onLine != nil {
				onLine(line)
			}
		}
		io.Copy(io.Discard, pr)
	}()
	go func() {
		p.err = cmd.Wait()
		pw.Close()
		<-scanDone
		close(p.done)
		if exits != nil {
			exits <- exitEvent{proc: p}
		}
	}()
	return p, nil
}

func (p *process) pid() int { return p.cmd.Process.Pid }

func (p *process) exited() bool {
	select {
	case <-p.done:
		return true
	default:
		return false
	}
}

func (p *process) signal(sig os.Signal) error {
	if p.exited() {
		return nil
	}
	return p.cmd.Process.Signal(sig)
}

// stop sends SIGTERM and escalates to SIGKILL after timeout.
func (p *process) stop(timeout time.Duration) {
	if p == nil || p.exited() {
		return
	}
	_ = p.signal(syscall.SIGTERM)
	select {
	case <-p.done:
	case <-time.After(timeout):
		_ = p.cmd.Process.Kill()
		<-p.done
	}
}

// describeExit renders a child's exit status.
func (p *process) describeExit() string {
	if p.err == nil {
		return "exited with status 0"
	}
	var ee *exec.ExitError
	if errors.As(p.err, &ee) {
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return fmt.Sprintf("was terminated by signal %d (%v)", int(ws.Signal()), ws.Signal())
		}
		return fmt.Sprintf("exited with status %d", ee.ExitCode())
	}
	return p.err.Error()
}
