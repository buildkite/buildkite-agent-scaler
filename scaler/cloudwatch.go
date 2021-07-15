package scaler

import (
	"log"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/cloudwatch"
)

const (
	cloudWatchMetricsNamespace = "Buildkite"
)

// cloudWatchMetricsPublisher sends queue metrics to AWS CloudWatch
type cloudWatchMetricsPublisher struct {
	sess *session.Session
}

// Publish queue metrics to CloudWatch Metrics
func (cp *cloudWatchMetricsPublisher) Publish(orgSlug, queue string, metrics map[string]int64) error {
	svc := cloudwatch.New(cp.sess)

	datum := []*cloudwatch.MetricDatum{}

	for k, v := range metrics {
		log.Printf("Publishing metric %s=%d [org=%s,queue=%s]",
			k, v, orgSlug, queue)

		datum = append(datum, &cloudwatch.MetricDatum{
			MetricName: aws.String(k),
			Unit:       aws.String("Count"),
			Value:      aws.Float64(float64(v)),
			Dimensions: []*cloudwatch.Dimension{
				&cloudwatch.Dimension{
					Name:  aws.String("Org"),
					Value: aws.String(orgSlug),
				},
				&cloudwatch.Dimension{
					Name:  aws.String("Queue"),
					Value: aws.String(queue),
				},
			},
		})
	}

	_, err := svc.PutMetricData(&cloudwatch.PutMetricDataInput{
		Namespace:  aws.String(cloudWatchMetricsNamespace),
		MetricData: datum,
	})

	return err
}

type dryRunMetricsPublisher struct {
}

func (p *dryRunMetricsPublisher) Publish(orgSlug, queue string, metrics map[string]int64) error {
	for k, v := range metrics {
		log.Printf("Publishing metric %s=%d", k, v)
	}
	return nil
}
