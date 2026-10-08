package gateway

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
)

// Logger serializes output from OnionForge itself and its child processes,
// tagging each line with its source ([onionforge], [tor], [caddy]).
type Logger struct {
	mu  sync.Mutex
	out io.Writer
}

// NewLogger writes to stdout.
func NewLogger() *Logger { return &Logger{out: os.Stdout} }

func (l *Logger) line(tag, msg string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, ln := range strings.Split(strings.TrimRight(msg, "\n"), "\n") {
		fmt.Fprintf(l.out, "[%s] %s\n", tag, ln)
	}
}

// Infof logs an OnionForge lifecycle message.
func (l *Logger) Infof(format string, args ...any) {
	l.line("onionforge", fmt.Sprintf(format, args...))
}

// Warnf logs a warning.
func (l *Logger) Warnf(format string, args ...any) {
	l.line("onionforge", "WARNING: "+fmt.Sprintf(format, args...))
}

// Errorf logs an error.
func (l *Logger) Errorf(format string, args ...any) {
	l.line("onionforge", "ERROR: "+fmt.Sprintf(format, args...))
}

// Raw writes text verbatim (used for the banner).
func (l *Logger) Raw(text string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	io.WriteString(l.out, text)
}

// Child logs a line from a child process.
func (l *Logger) Child(tag, line string) { l.line(tag, line) }
