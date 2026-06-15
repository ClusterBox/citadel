package parsers

import (
	"encoding/json"
	"regexp"
	"strings"

	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
)

// GoLambda parses log lines from clusterbox Go-based Lambda functions (smaug).
// Two shapes are flagged:
//
//  1. structured slog lines with level=ERROR / FATAL
//  2. APIGW REPORT lines carrying a 5xx response status
//
// The parser is intentionally stateless: a single Parser is shared across
// services and stream interleaving (events from many streams come back from
// one FilterLogEvents call) makes "remember the last START line" unsafe.
// Request-ID correlation therefore comes from the line itself — the requestId
// field in a slog payload, or the RequestId substring already embedded in the
// REPORT line we extract.
type GoLambda struct{}

type slogLine struct {
	Level     string `json:"level"`
	Msg       string `json:"msg"`
	Message   string `json:"message"`
	RequestID string `json:"requestId"`
	Stack     string `json:"stack"`
	Error     string `json:"error"`
}

// apigwStatusRE matches "Status: 5xx" anywhere in the line (CloudFront and API
// Gateway both emit access-log style lines containing this).
var apigwStatusRE = regexp.MustCompile(`(?i)status[:=]\s?(\d{3})`)

// requestIDRE pulls a request id from any Lambda boilerplate line
// (START / END / REPORT all contain "RequestId: <uuid>").
var requestIDRE = regexp.MustCompile(`(?i)RequestId:\s+([A-Za-z0-9-]+)`)

func (GoLambda) Parse(event cwtypes.FilteredLogEvent) (ErrorEvent, bool) {
	if event.Message == nil {
		return ErrorEvent{}, false
	}
	raw := *event.Message
	trimmed := strings.TrimSpace(raw)

	// START lines never count as errors, but we still ignore them cleanly.
	if strings.HasPrefix(trimmed, "START RequestId") ||
		strings.HasPrefix(trimmed, "END RequestId") ||
		strings.HasPrefix(trimmed, "REPORT RequestId") {
		// REPORT lines occasionally carry a 5xx status field.
		if m := apigwStatusRE.FindStringSubmatch(trimmed); m != nil {
			if m[1][0] == '5' {
				return ErrorEvent{
					TS:        deref(event.Timestamp),
					Status:    atoi(m[1]),
					Level:     "error",
					Message:   "Lambda 5xx response",
					Raw:       raw,
					LogStream: derefString(event.LogStreamName),
					CWEventID: derefString(event.EventId),
					RequestID: extractRequestID(trimmed),
				}, true
			}
		}
		return ErrorEvent{}, false
	}

	// slog JSON
	if strings.HasPrefix(trimmed, "{") {
		var s slogLine
		if err := json.Unmarshal([]byte(trimmed), &s); err == nil {
			if strings.EqualFold(s.Level, "error") || strings.EqualFold(s.Level, "fatal") {
				msg := firstNonEmpty(s.Msg, s.Message, s.Error, "(no message)")
				return ErrorEvent{
					TS:        deref(event.Timestamp),
					Level:     strings.ToLower(s.Level),
					Message:   msg,
					RequestID: s.RequestID,
					Stack:     s.Stack,
					Raw:       raw,
					LogStream: derefString(event.LogStreamName),
					CWEventID: derefString(event.EventId),
				}, true
			}
			return ErrorEvent{}, false
		}
	}

	// Free-form runtime panic lines ("[ERROR] ..." or stderr output).
	upper := strings.ToUpper(trimmed)
	if strings.Contains(upper, "[ERROR]") || strings.Contains(upper, "PANIC:") || strings.Contains(upper, "RUNTIME ERROR") {
		return ErrorEvent{
			TS:        deref(event.Timestamp),
			Level:     "error",
			Message:   trimmed,
			Raw:       raw,
			LogStream: derefString(event.LogStreamName),
			CWEventID: derefString(event.EventId),
		}, true
	}

	return ErrorEvent{}, false
}

func extractRequestID(line string) string {
	if m := requestIDRE.FindStringSubmatch(line); m != nil {
		return m[1]
	}
	return ""
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}
