package initcmd

import (
	"context"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

type envDetector struct{}

// NewEnvDetector returns a Detector that reads AWS_REGION / AWS_DEFAULT_REGION
// and asks STS GetCallerIdentity for the account.
func NewEnvDetector() Detector { return envDetector{} }

func (envDetector) Region() string {
	for _, k := range []string{"AWS_REGION", "AWS_DEFAULT_REGION"} {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return ""
}

// Account bounds the lookup at 10s: with no credentials the SDK may probe
// instance metadata, and init should fall back to a prompt quickly.
func (envDetector) Account(ctx context.Context, region string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
	if err != nil {
		return "", err
	}
	out, err := sts.NewFromConfig(cfg).GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return "", err
	}
	return aws.ToString(out.Account), nil
}
