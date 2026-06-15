package parsers

// ErrorEvent is the parser output that the ingest loop persists. Optional
// fields use empty values rather than pointers so parsers can build them
// inline without nil-juggling.
type ErrorEvent struct {
	// TS is milliseconds since epoch, taken from CloudWatch's event Timestamp.
	TS int64
	// Status is the HTTP status code if the parser found one (e.g. 500, 502).
	// Zero means the parser flagged the line on level alone, not a status.
	Status int
	// Level is "error" or "warn" — informational, not used for filtering.
	Level string
	// Message is the human-readable one-liner shown in the dashboard list.
	Message string
	// RequestID is the correlation id (APIGW requestId, Nest x-request-id) if
	// the parser extracted it.
	RequestID string
	// Stack is the full stack trace if present in the log line.
	Stack string
	// Raw is the unmodified log line for the "show raw" toggle.
	Raw string
	// LogStream is CloudWatch's logStreamName for the event.
	LogStream string
	// CWEventID is CloudWatch's per-event id, used as the dedupe key.
	CWEventID string
}
