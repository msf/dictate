package output

import (
	"fmt"
	"os"
	"sync"
)

// Sink receives transcribed text.
type Sink interface {
	Write(text string)
	Close() error
}

// StdoutSink writes text to stdout.
type StdoutSink struct{}

func (s StdoutSink) Write(text string) {
	fmt.Println(text)
}

func (s StdoutSink) Close() error { return nil }

// FileSink appends transcribed text to a file.
type FileSink struct {
	f  *os.File
	mu sync.Mutex
}

func NewFileSink(path string) (*FileSink, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	return &FileSink{f: f}, nil
}

func (s *FileSink) Write(text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fmt.Fprintln(s.f, text)
	_ = s.f.Sync()
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

func (m *MultiSink) Write(text string) {
	for _, s := range m.sinks {
		s.Write(text)
	}
}

func (m *MultiSink) Close() error {
	for _, s := range m.sinks {
		s.Close()
	}
	return nil
}
