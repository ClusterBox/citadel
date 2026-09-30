package docker

import (
	"bytes"
	"strings"
	"testing"
)

// The daemon reports a failed build or push inside a 200 response, as a
// JSON message with "error"/"errorDetail". A deploy must stop there, not
// report the image as pushed and roll out an image that does not exist.
func TestCopyDaemonStreamFailsOnErrorMessage(t *testing.T) {
	stream := `{"status":"The push refers to repository [x.dkr.ecr.us-east-1.amazonaws.com/app-dev-repo]"}
{"status":"Preparing","id":"abc"}
{"errorDetail":{"message":"error from registry: The repository with name 'app-dev-repo' does not exist"},"error":"error from registry: The repository with name 'app-dev-repo' does not exist"}
`
	var out bytes.Buffer
	err := copyDaemonStream(&out, strings.NewReader(stream))
	if err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("err = %v, want the daemon's error", err)
	}
	if !strings.Contains(out.String(), "Preparing") {
		t.Errorf("progress before the error was not shown: %q", out.String())
	}
}

func TestCopyDaemonStreamPassesSuccessThrough(t *testing.T) {
	stream := `{"stream":"Step 1/2 : FROM alpine\n"}
{"aux":{"ID":"sha256:abc"}}
{"status":"latest: digest: sha256:def size: 1234"}
`
	var out bytes.Buffer
	if err := copyDaemonStream(&out, strings.NewReader(stream)); err != nil {
		t.Fatalf("err = %v", err)
	}
	if out.String() != stream {
		t.Errorf("output altered:\n got %q\nwant %q", out.String(), stream)
	}
}
