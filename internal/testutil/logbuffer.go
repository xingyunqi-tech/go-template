package testutil

import (
	"bytes"
	"strings"
	"sync"
)

// LogBuffer collects log output written from several goroutines.
type LogBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

// Write implements io.Writer.
func (b *LogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.Write(p) //nolint:wrapcheck // bytes.Buffer never fails
}

// Lines returns the lines written so far that contain substr.
func (b *LogBuffer) Lines(substr string) []string {
	b.mu.Lock()
	defer b.mu.Unlock()

	var out []string

	for line := range strings.Lines(b.buf.String()) {
		if strings.Contains(line, substr) {
			out = append(out, strings.TrimSpace(line))
		}
	}

	return out
}
