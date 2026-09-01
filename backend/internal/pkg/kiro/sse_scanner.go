package kiro

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"strings"
)

// SSEScanner parses Server-Sent Events (SSE) from a stream.
// It follows the SSE specification: https://html.spec.whatwg.org/multipage/server-sent-events.html
type SSEScanner struct {
	scanner *bufio.Scanner
	event   string // Current event type
	data    []byte // Current event data (concatenated if multiple data lines)
	err     error  // Last error encountered
}

// NewSSEScanner creates a new SSE scanner from a reader.
func NewSSEScanner(r io.Reader) *SSEScanner {
	scanner := bufio.NewScanner(r)
	return &SSEScanner{
		scanner: scanner,
	}
}

// Scan advances to the next SSE event.
// It returns false when the scan stops, either by reaching the end of the input or an error.
func (s *SSEScanner) Scan() bool {
	s.event = ""
	s.data = nil

	var dataLines [][]byte

	for s.scanner.Scan() {
		line := s.scanner.Bytes()

		// Empty line = end of event
		if len(line) == 0 {
			if s.event != "" || len(dataLines) > 0 {
				// Concatenate data lines with newlines
				if len(dataLines) > 0 {
					s.data = bytes.Join(dataLines, []byte("\n"))
				}
				return true
			}
			// Skip consecutive empty lines
			continue
		}

		// Ignore comment lines (start with ':')
		if line[0] == ':' {
			continue
		}

		// Parse field:value
		colonIdx := bytes.IndexByte(line, ':')
		if colonIdx == -1 {
			// Field with no value (e.g., "event" alone) - ignore
			continue
		}

		field := string(line[:colonIdx])
		value := line[colonIdx+1:]

		// Skip leading space after colon
		if len(value) > 0 && value[0] == ' ' {
			value = value[1:]
		}

		switch field {
		case "event":
			s.event = string(value)
		case "data":
			dataLines = append(dataLines, value)
		case "id", "retry":
			// We don't use these fields, but they're part of SSE spec
		}
	}

	// Check for scanner error
	if err := s.scanner.Err(); err != nil {
		s.err = err
		return false
	}

	// End of stream
	// If we have accumulated data, return it as the last event
	if s.event != "" || len(dataLines) > 0 {
		if len(dataLines) > 0 {
			s.data = bytes.Join(dataLines, []byte("\n"))
		}
		return true
	}

	return false
}

// Event returns the current event type (e.g., "message_start", "content_block_delta").
// If no event type was specified, returns empty string.
func (s *SSEScanner) Event() string {
	return s.event
}

// Data returns the current event data as raw bytes.
// Multiple data lines are concatenated with newlines.
func (s *SSEScanner) Data() []byte {
	return s.data
}

// Err returns the first non-EOF error that was encountered by the scanner.
func (s *SSEScanner) Err() error {
	return s.err
}

// SSEEvent represents a parsed SSE event with both event type and data.
type SSEEvent struct {
	Event string // Event type (e.g., "message_start")
	Data  []byte // Event data payload
}

// String returns a human-readable representation of the event.
func (e SSEEvent) String() string {
	eventType := e.Event
	if eventType == "" {
		eventType = "(unnamed)"
	}
	dataPreview := string(e.Data)
	if len(dataPreview) > 80 {
		dataPreview = dataPreview[:77] + "..."
	}
	return fmt.Sprintf("SSEEvent{event=%q, data=%q}", eventType, dataPreview)
}

// FormatSSE formats an SSE event for writing to a stream.
// It follows the SSE specification format.
func FormatSSE(event string, data []byte) []byte {
	var buf bytes.Buffer

	// Write event line if specified
	if event != "" {
		buf.WriteString("event: ")
		buf.WriteString(event)
		buf.WriteByte('\n')
	}

	// Write data line(s)
	// If data contains newlines, split into multiple data: lines
	dataStr := string(data)
	if strings.Contains(dataStr, "\n") {
		for _, line := range strings.Split(dataStr, "\n") {
			buf.WriteString("data: ")
			buf.WriteString(line)
			buf.WriteByte('\n')
		}
	} else {
		buf.WriteString("data: ")
		buf.Write(data)
		buf.WriteByte('\n')
	}

	// Empty line to end the event
	buf.WriteByte('\n')

	return buf.Bytes()
}
