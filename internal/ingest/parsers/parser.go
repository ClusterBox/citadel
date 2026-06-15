// Package parsers contains per-runtime CloudWatch log-line parsers used by
// the citadel-logs ingest loop. Each parser inspects a single FilteredLogEvent
// and either returns an ErrorEvent (the line described an error) or signals
// "ignore this line."
package parsers

import (
	"fmt"

	"github.com/ClusterBox/citadel/pkg/config"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
)

// Parser inspects one log event. The boolean return reports whether the line
// matched an error pattern; when false, ev is undefined and should be
// discarded.
type Parser interface {
	Parse(event cwtypes.FilteredLogEvent) (ev ErrorEvent, ok bool)
}

// ForRuntime returns the parser registered for the given runtime. The daemon
// fails fast at startup if a registered service references an unknown runtime
// rather than silently dropping its logs.
func ForRuntime(rt config.Runtime) (Parser, error) {
	switch rt {
	case config.RuntimeECS:
		return NestJS{}, nil
	case config.RuntimeLambda:
		return GoLambda{}, nil
	default:
		return nil, fmt.Errorf("no parser registered for runtime %q", rt)
	}
}
