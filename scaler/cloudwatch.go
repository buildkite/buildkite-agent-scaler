package scaler

import (
	"context"
	"log"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
)

const (
	cloudWatchMetricsNamespace = "Buildkite"
)

// cloudWatchMetricsPublisher sends queue metrics to AWS CloudWatch
type cloudWatchMetricsPublisher struct {
	cfg aws.Config
}

// Publish queue metrics to CloudWatch Metrics
// The context allows for request cancellation and timeouts.
func (cp *cloudWatchMetricsPublisher) Publish(ctx context.Context, orgSlug, queue string, metrics map[string]int64) error {
	svc := cloudwatch.NewFromConfig(cp.cfg)

	datum := make([]types.MetricDatum, 0, len(metrics))

	for k, v := range metrics {
		log.Printf("Publishing metric %s=%d [org=%s,queue=%s]",
			k, v, orgSlug, queue)

		datum = append(datum, types.MetricDatum{
			MetricName: aws.String(k),
			Unit:       types.StandardUnitCount,
			Value:      aws.Float64(float64(v)),
			Dimensions: []types.Dimension{
				{
					Name:  aws.String("Org"),
					Value: aws.String(orgSlug),
				},
				{
					Name:  aws.String("Queue"),
					Value: aws.String(queue),
				},
			},
		})
	}

	_, err := svc.PutMetricData(ctx, &cloudwatch.PutMetricDataInput{
		Namespace:  aws.String(cloudWatchMetricsNamespace),
		MetricData: datum,
	})

	return err
}

type dryRunMetricsPublisher struct{}

func (p *dryRunMetricsPublisher) Publish(ctx context.Context, orgSlug, queue string, metrics map[string]int64) error {
	for k, v := range metrics {
		log.Printf("[DRY RUN] Would publish metric %s=%d [org=%s,queue=%s]", k, v, orgSlug, queue)
	}
	return nil
}
