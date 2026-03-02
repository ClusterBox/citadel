package aws

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/ClusterBox/citadel/pkg/config"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
)

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

// StreamLogs tails CloudWatch logs for the ECS service
func (lc *LogsClient) StreamLogs(ctx context.Context, cfg *config.DeployConfig, tailLines int) error {
	logGroupName := fmt.Sprintf("/ecs/%s", cfg.Name)

	// Get the most recent log streams
	streamsOutput, err := lc.client.DescribeLogStreams(ctx, &cloudwatchlogs.DescribeLogStreamsInput{
		LogGroupName: aws.String(logGroupName),
		OrderBy:      "LastEventTime",
		Descending:   aws.Bool(true),
		Limit:        aws.Int32(5),
	})
	if err != nil {
		return fmt.Errorf("failed to describe log streams: %w", err)
	}

	if len(streamsOutput.LogStreams) == 0 {
		return fmt.Errorf("no log streams found in %s", logGroupName)
	}

	// Start time: look back far enough to get tailLines
	startTime := time.Now().Add(-30 * time.Minute).UnixMilli()

	// Collect stream names
	var streamNames []string
	for _, stream := range streamsOutput.LogStreams {
		streamNames = append(streamNames, *stream.LogStreamName)
	}

	// Print initial batch of recent events
	fmt.Printf("--- Log group: %s ---\n", logGroupName)
	fmt.Printf("--- Streams: %d active ---\n\n", len(streamNames))

	nextTokens := make(map[string]*string)

	// Fetch initial events from each stream
	for _, streamName := range streamNames {
		events, nextToken, err := lc.getLogEvents(ctx, logGroupName, streamName, startTime, nil)
		if err != nil {
			fmt.Printf("Warning: failed to read stream %s: %v\n", streamName, err)
			continue
		}
		nextTokens[streamName] = nextToken

		for _, event := range events {
			ts := time.UnixMilli(*event.Timestamp)
			fmt.Printf("[%s] %s\n", ts.Format("15:04:05"), *event.Message)
		}
	}

	// Live tail loop
	fmt.Printf("\n--- Live tailing (Ctrl+C to exit) ---\n\n")

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		hasNew := false
		for _, streamName := range streamNames {
			events, nextToken, err := lc.getLogEvents(ctx, logGroupName, streamName, 0, nextTokens[streamName])
			if err != nil {
				continue
			}
			nextTokens[streamName] = nextToken

			for _, event := range events {
				ts := time.UnixMilli(*event.Timestamp)
				fmt.Printf("[%s] %s\n", ts.Format("15:04:05"), *event.Message)
				hasNew = true
			}
		}

		if !hasNew {
			time.Sleep(2 * time.Second)
		}
	}
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
func (lc *LogsClient) GetRecentLogs(ctx context.Context, cfg *config.DeployConfig, lines int) error {
	logGroupName := fmt.Sprintf("/ecs/%s", cfg.Name)

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
