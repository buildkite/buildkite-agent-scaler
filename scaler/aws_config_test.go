package scaler

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
)

func TestLoadAWSConfigUsesAdaptiveRetryer(t *testing.T) {
	t.Setenv("AWS_REGION", "us-east-1")
	// Clear env-var overrides so the test observes the code-level defaults.
	t.Setenv("AWS_RETRY_MODE", "")
	t.Setenv("AWS_MAX_ATTEMPTS", "")

	cfg, err := LoadAWSConfig(context.Background())
	if err != nil {
		t.Fatalf("LoadAWSConfig returned error: %v", err)
	}

	if cfg.RetryMode != aws.RetryModeAdaptive {
		t.Errorf("cfg.RetryMode = %q, want %q", cfg.RetryMode, aws.RetryModeAdaptive)
	}
	if cfg.RetryMaxAttempts != 8 {
		t.Errorf("cfg.RetryMaxAttempts = %d, want 8", cfg.RetryMaxAttempts)
	}
}

func TestLoadAWSConfigHonorsEnvOverrides(t *testing.T) {
	t.Setenv("AWS_REGION", "us-east-1")
	t.Setenv("AWS_RETRY_MODE", "standard")
	t.Setenv("AWS_MAX_ATTEMPTS", "3")

	cfg, err := LoadAWSConfig(context.Background())
	if err != nil {
		t.Fatalf("LoadAWSConfig returned error: %v", err)
	}

	if cfg.RetryMode != aws.RetryModeStandard {
		t.Errorf("env override AWS_RETRY_MODE=standard ignored: cfg.RetryMode = %q", cfg.RetryMode)
	}
	if cfg.RetryMaxAttempts != 3 {
		t.Errorf("env override AWS_MAX_ATTEMPTS=3 ignored: cfg.RetryMaxAttempts = %d", cfg.RetryMaxAttempts)
	}
}
