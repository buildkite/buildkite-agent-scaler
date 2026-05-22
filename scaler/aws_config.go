package scaler

import (
	"context"
	"os"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
)

// LoadAWSConfig loads the AWS SDK config with adaptive retry mode and 8 max
// attempts. The SDK default (3 attempts, no client-side rate limiting) gives
// up on throttling errors when many scaler Lambdas hit the same APIs at once.
//
// Set AWS_RETRY_MODE or AWS_MAX_ATTEMPTS to override.
func LoadAWSConfig(ctx context.Context) (aws.Config, error) {
	var opts []func(*config.LoadOptions) error

	if os.Getenv("AWS_RETRY_MODE") == "" {
		opts = append(opts, config.WithRetryMode(aws.RetryModeAdaptive))
	}
	if os.Getenv("AWS_MAX_ATTEMPTS") == "" {
		opts = append(opts, config.WithRetryMaxAttempts(8))
	}

	return config.LoadDefaultConfig(ctx, opts...)
}
