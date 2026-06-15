package parsers

import (
	"encoding/json"
	"strings"

	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
)

// NestJS parses log lines from clusterbox NestJS backends (Aragorn, gollum).
// Their Logger emits JSON with a stable shape; non-JSON Nest CLI banner lines
// are ignored. A line is considered an error if statusCode is 5xx, level is
// "error", or it carries an Exception field.
type NestJS struct{}

type nestLine struct {
	Level      string          `json:"level"`
	Message    json.RawMessage `json:"message"`
	StatusCode int             `json:"statusCode"`
	RequestID  string          `json:"requestId"`
	Path       string          `json:"path"`
	Method     string          `json:"method"`
	Stack      string          `json:"stack"`
	Exception  string          `json:"exception"`
}

func (NestJS) Parse(event cwtypes.FilteredLogEvent) (ErrorEvent, bool) {
	if event.Message == nil {
		return ErrorEvent{}, false
	}
	raw := *event.Message
	// Cheap pre-check: avoid JSON parsing the vast majority of non-JSON lines.
	trimmed := strings.TrimSpace(raw)
	if !strings.HasPrefix(trimmed, "{") {
		return ErrorEvent{}, false
	}
	var n nestLine
	if err := json.Unmarshal([]byte(trimmed), &n); err != nil {
		return ErrorEvent{}, false
	}

	isError := n.StatusCode >= 500 || strings.EqualFold(n.Level, "error") || n.Exception != ""
	if !isError {
		return ErrorEvent{}, false
	}

	msg := decodeMessage(n.Message)
	if msg == "" {
		if n.Exception != "" {
			msg = n.Exception
		} else {
			msg = "(no message)"
		}
	}
	if n.Method != "" && n.Path != "" {
		msg = n.Method + " " + n.Path + " — " + msg
	}

	ev := ErrorEvent{
		TS:        deref(event.Timestamp),
		Status:    n.StatusCode,
		Level:     strings.ToLower(n.Level),
		Message:   msg,
		RequestID: n.RequestID,
		Stack:     n.Stack,
		Raw:       raw,
		LogStream: derefString(event.LogStreamName),
		CWEventID: derefString(event.EventId),
	}
	if ev.Level == "" {
		ev.Level = "error"
	}
	return ev, true
}

// decodeMessage tolerates Nest's habit of sometimes putting message as a string
// and sometimes as an object {message, error, ...}.
func decodeMessage(m json.RawMessage) string {
	if len(m) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(m, &s); err == nil {
		return s
	}
	var obj struct {
		Message string `json:"message"`
		Error   string `json:"error"`
	}
	if err := json.Unmarshal(m, &obj); err == nil {
		if obj.Message != "" {
			return obj.Message
		}
		return obj.Error
	}
	return string(m)
}

func deref(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

func derefString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
