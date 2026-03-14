package output

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
)

// Sink receives transcribed text.
type Sink interface {
	Write(raw, display string)
	Close() error
}

// StdoutSink writes text to stdout.
type StdoutSink struct{}

func (s StdoutSink) Write(_, display string) {
	fmt.Println(display)
}

func (s StdoutSink) Close() error { return nil }

// TypeSink injects text into the currently focused Wayland text input via wtype.
type TypeSink struct {
	path string
	mu   sync.Mutex
}

func NewTypeSink() (*TypeSink, error) {
	path, err := exec.LookPath("wtype")
	if err != nil {
		return nil, fmt.Errorf("find wtype: %w", err)
	}
	return &TypeSink{path: path}, nil
}

func (s *TypeSink) Write(raw, _ string) {
	chunk := formatTypedChunk(raw)
	if chunk == "" {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	cmd := exec.Command(s.path, "-")
	cmd.Stdin = strings.NewReader(chunk)
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "dictate: wtype failed: %v\n", err)
	}
}

func (s *TypeSink) Close() error { return nil }

// FileSink appends transcribed text to a file.
type FileSink struct {
	f   *os.File
	mu  sync.Mutex
	raw bool
}

func NewFileSink(path string) (*FileSink, error) {
	return newFileSink(path, false)
}

func NewRawFileSink(path string) (*FileSink, error) {
	return newFileSink(path, true)
}

func newFileSink(path string, raw bool) (*FileSink, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	return &FileSink{f: f, raw: raw}, nil
}

func (s *FileSink) Write(raw, display string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	text := display
	if s.raw {
		text = raw
	}
	fmt.Fprintln(s.f, text)
}

func (s *FileSink) Close() error {
	return s.f.Close()
}

// MultiSink fans out to multiple sinks.
type MultiSink struct {
	sinks []Sink
}

func NewMultiSink(sinks ...Sink) *MultiSink {
	return &MultiSink{sinks: sinks}
}

func (m *MultiSink) Write(raw, display string) {
	for _, s := range m.sinks {
		s.Write(raw, display)
	}
}

func (m *MultiSink) Close() error {
	var errs []error
	for _, s := range m.sinks {
		errs = append(errs, s.Close())
	}
	return errors.Join(errs...)
}

func formatTypedChunk(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	return text + " "
}
