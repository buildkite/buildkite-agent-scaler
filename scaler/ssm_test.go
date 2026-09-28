package scaler

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
)

func TestNewSSMClientRegion(t *testing.T) {
	tests := []struct {
		name      string
		cfgRegion string
		region    string
		want      string
	}{
		{
			name:      "default inherits config region when override is empty",
			cfgRegion: "us-east-1",
			region:    "",
			want:      "us-east-1",
		},
		{
			name:      "explicit override wins over config region",
			cfgRegion: "us-east-1",
			region:    "eu-west-2",
			want:      "eu-west-2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := aws.Config{Region: tt.cfgRegion}

			client := newSSMClient(cfg, tt.region)

			if got := client.Options().Region; got != tt.want {
				t.Errorf("client region = %q, want %q", got, tt.want)
			}
			// The shared config must not be mutated by the override.
			if cfg.Region != tt.cfgRegion {
				t.Errorf("cfg.Region mutated to %q, want %q", cfg.Region, tt.cfgRegion)
			}
		})
	}
}
