package sse

import (
	"strings"
	"testing"
)

func TestEncodeEvent(t *testing.T) {
	got, err := EncodeEvent("message_delta", map[string]any{"type": "message_delta"})
	if err != nil {
		t.Fatal(err)
	}
	want := "event: message_delta\ndata: {\"type\":\"message_delta\"}\n\n"
	if got != want {
		t.Fatalf("event = %q, want %q", got, want)
	}
}

func TestParseAllHandlesMultilineDataAndComments(t *testing.T) {
	events, err := ParseAll(strings.NewReader(": comment\n\nevent: one\ndata: {\"a\":1}\n\nevent: two\ndata: line1\ndata: line2\n\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("events len = %d, want 2", len(events))
	}
	if events[0].Event != "one" || events[0].Data != `{"a":1}` {
		t.Fatalf("event[0] = %+v", events[0])
	}
	if events[1].Event != "two" || events[1].Data != "line1\nline2" {
		t.Fatalf("event[1] = %+v", events[1])
	}
}

func TestParseAllHandlesLargeDataLines(t *testing.T) {
	// Codex can emit very large SSE data lines (e.g. encrypted reasoning content
	// or a tool call with large arguments). Anything below MaxLineBytes must
	// parse without bufio.Scanner returning ErrTooLong.
	const size = 8 * 1024 * 1024
	payload := strings.Repeat("a", size)
	events, err := ParseAll(strings.NewReader("event: huge\ndata: " + payload + "\n\n"))
	if err != nil {
		t.Fatalf("parse failed for %d byte data line: %v", size, err)
	}
	if len(events) != 1 || events[0].Event != "huge" {
		t.Fatalf("expected single huge event, got %d events", len(events))
	}
	if len(events[0].Data) != size {
		t.Fatalf("data length = %d, want %d", len(events[0].Data), size)
	}
}

func TestParseAllFlushesTrailingEventWithoutBlankLine(t *testing.T) {
	events, err := ParseAll(strings.NewReader("event: done\ndata: ok"))
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Event != "done" || events[0].Data != "ok" {
		t.Fatalf("events = %+v", events)
	}
}
