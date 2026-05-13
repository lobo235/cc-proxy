package sse

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

type Event struct {
	Event string
	Data  string
}

func EncodeEvent(event string, data any) (string, error) {
	encoded, err := json.Marshal(data)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("event: %s\ndata: %s\n\n", event, encoded), nil
}

func ParseAll(r io.Reader) ([]Event, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	var events []Event
	var event string
	var data []string
	flush := func() {
		if event == "" && len(data) == 0 {
			return
		}
		events = append(events, Event{Event: event, Data: strings.Join(data, "\n")})
		event = ""
		data = nil
	}
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if line == "" {
			flush()
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, ok := strings.Cut(line, ":")
		if ok {
			value = strings.TrimPrefix(value, " ")
		}
		switch field {
		case "event":
			event = value
		case "data":
			data = append(data, value)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	flush()
	return events, nil
}
