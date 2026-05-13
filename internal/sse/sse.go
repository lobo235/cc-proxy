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
	var events []Event
	err := Parse(r, func(event Event) error {
		events = append(events, event)
		return nil
	})
	return events, err
}

func Parse(r io.Reader, handle func(Event) error) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	var event string
	var data []string
	flush := func() error {
		if event == "" && len(data) == 0 {
			return nil
		}
		if handle != nil {
			if err := handle(Event{Event: event, Data: strings.Join(data, "\n")}); err != nil {
				return err
			}
		}
		event = ""
		data = nil
		return nil
	}
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if line == "" {
			if err := flush(); err != nil {
				return err
			}
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
		return err
	}
	return flush()
}
