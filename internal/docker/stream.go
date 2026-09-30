package docker

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// copyDaemonStream copies the daemon's JSON message stream (build or push
// output) to w and returns the first error the daemon reports in it. The
// daemon answers a failed build or push with HTTP 200 and an "error" message
// in the stream, so the transport succeeding says nothing about the result.
func copyDaemonStream(w io.Writer, r io.Reader) error {
	dec := json.NewDecoder(io.TeeReader(r, w))
	for {
		var msg struct {
			Error       string `json:"error"`
			ErrorDetail *struct {
				Message string `json:"message"`
			} `json:"errorDetail"`
		}
		if err := dec.Decode(&msg); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("reading daemon output: %w", err)
		}
		if msg.ErrorDetail != nil && msg.ErrorDetail.Message != "" {
			return errors.New(msg.ErrorDetail.Message)
		}
		if msg.Error != "" {
			return errors.New(msg.Error)
		}
	}
}
