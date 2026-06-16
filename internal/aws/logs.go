package aws

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
)

// FilterEventsPage is one page of results from FilterLogEvents, used by the
// citadel-logs ingest loop. Events are NOT pre-sorted; callers should respect
// the Timestamp on each event when persisting cursors.
type FilterEventsPage struct {
	Events    []cwtypes.FilteredLogEvent
	NextToken *string
}

// FilterEvents fetches one page of events from logGroup with ts >= startMs
// and ts < endMs. nextToken is forwarded transparently for pagination.
// Limit is clamped to the CloudWatch maximum (10000); 0 means default (1000).
func (lc *LogsClient) FilterEvents(ctx context.Context, logGroup string, startMs, endMs int64, limit int32, nextToken *string) (*FilterEventsPage, error) {
	if limit == 0 {
		limit = 1000
	}
	input := &cloudwatchlogs.FilterLogEventsInput{
		LogGroupName: aws.String(logGroup),
		StartTime:    aws.Int64(startMs),
		EndTime:      aws.Int64(endMs),
		Limit:        aws.Int32(limit),
	}
	if nextToken != nil {
		input.NextToken = nextToken
	}
	out, err := lc.client.FilterLogEvents(ctx, input)
	if err != nil {
		return nil, err
	}
	return &FilterEventsPage{Events: out.Events, NextToken: out.NextToken}, nil
}

// LogsClient wraps CloudWatch Logs operations
type LogsClient struct {
	client *cloudwatchlogs.Client
}

// NewLogsClient creates a new CloudWatch Logs client
func (c *Client) NewLogsClient() *LogsClient {
	return &LogsClient{
		client: cloudwatchlogs.NewFromConfig(c.Config),
	}
}

// StreamLogs tails CloudWatch logs for the given log group.
//
// It uses FilterLogEvents, which spans every stream in the group, so streams
// created after tailing starts (e.g. the new task started by a deploy) are
// picked up automatically — unlike a fixed per-stream poll, which silently goes
// stale once the running task rotates.
func (lc *LogsClient) StreamLogs(ctx context.Context, logGroupName string, tailLines int) error {
	fmt.Printf("--- Log group: %s ---\n", logGroupName)
	fmt.Printf("--- Live tailing (Ctrl+C to exit) ---\n\n")

	// Look back over a recent window for the initial batch, then advance the
	// cursor strictly forward as events arrive. cursor is the inclusive lower
	// bound (epoch ms) for the next FilterLogEvents query.
	cursor := time.Now().Add(-30 * time.Minute).UnixMilli()

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		// EndTime is exclusive; +1 includes events at exactly "now".
		end := time.Now().UnixMilli() + 1
		if end <= cursor {
			time.Sleep(2 * time.Second)
			continue
		}

		newest, err := lc.printWindow(ctx, logGroupName, cursor, end)
		if err != nil {
			// Transient (throttling, etc.) — retry next tick without advancing
			// the cursor so nothing is skipped.
			time.Sleep(2 * time.Second)
			continue
		}
		// Advance past the newest event printed so it isn't repeated.
		if newest >= cursor {
			cursor = newest + 1
		}
		time.Sleep(2 * time.Second)
	}
}

// printWindow fetches and prints every event in [startMs, endMs) across all
// streams in the group, paginating as needed, and returns the largest event
// timestamp seen (or startMs-1 when the window is empty).
func (lc *LogsClient) printWindow(ctx context.Context, logGroup string, startMs, endMs int64) (int64, error) {
	newest := startMs - 1
	var nextToken *string
	const pageCap = 50 // safety bound on pages per tick
	for pages := 0; pages < pageCap; pages++ {
		page, err := lc.FilterEvents(ctx, logGroup, startMs, endMs, 0, nextToken)
		if err != nil {
			return newest, err
		}
		for _, e := range page.Events {
			ts := aws.ToInt64(e.Timestamp)
			fmt.Printf("[%s] %s\n", time.UnixMilli(ts).Format("15:04:05"), aws.ToString(e.Message))
			if ts > newest {
				newest = ts
			}
		}
		if page.NextToken == nil {
			break
		}
		nextToken = page.NextToken
	}
	return newest, nil
}

// getLogEvents fetches log events from a specific stream
func (lc *LogsClient) getLogEvents(ctx context.Context, logGroup, streamName string, startTimeMs int64, nextToken *string) ([]logEvent, *string, error) {
	input := &cloudwatchlogs.GetLogEventsInput{
		LogGroupName:  aws.String(logGroup),
		LogStreamName: aws.String(streamName),
		StartFromHead: aws.Bool(false),
	}

	if nextToken != nil {
		input.NextToken = nextToken
	} else if startTimeMs > 0 {
		input.StartTime = aws.Int64(startTimeMs)
		input.StartFromHead = aws.Bool(true)
	}

	output, err := lc.client.GetLogEvents(ctx, input)
	if err != nil {
		return nil, nil, err
	}

	var events []logEvent
	for _, e := range output.Events {
		events = append(events, logEvent{
			Timestamp: e.Timestamp,
			Message:   e.Message,
		})
	}

	// Sort by timestamp
	sort.Slice(events, func(i, j int) bool {
		return *events[i].Timestamp < *events[j].Timestamp
	})

	return events, output.NextForwardToken, nil
}

type logEvent struct {
	Timestamp *int64
	Message   *string
}

// GetRecentLogs fetches the most recent log lines (non-streaming, for status command)
func (lc *LogsClient) GetRecentLogs(ctx context.Context, logGroupName string, lines int) error {
	streamsOutput, err := lc.client.DescribeLogStreams(ctx, &cloudwatchlogs.DescribeLogStreamsInput{
		LogGroupName: aws.String(logGroupName),
		OrderBy:      "LastEventTime",
		Descending:   aws.Bool(true),
		Limit:        aws.Int32(3),
	})
	if err != nil {
		return fmt.Errorf("failed to describe log streams: %w", err)
	}

	if len(streamsOutput.LogStreams) == 0 {
		fmt.Printf("   No log streams found\n")
		return nil
	}

	startTime := time.Now().Add(-10 * time.Minute).UnixMilli()
	printed := 0

	for _, stream := range streamsOutput.LogStreams {
		if printed >= lines {
			break
		}

		events, _, err := lc.getLogEvents(ctx, logGroupName, *stream.LogStreamName, startTime, nil)
		if err != nil {
			continue
		}

		for _, event := range events {
			if printed >= lines {
				break
			}
			ts := time.UnixMilli(*event.Timestamp)
			fmt.Printf("   [%s] %s\n", ts.Format("15:04:05"), *event.Message)
			printed++
		}
	}

	return nil
}
