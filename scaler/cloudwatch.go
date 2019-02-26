package scaler

import (
	"log"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/cloudwatch"
	"github.com/davecgh/go-spew/spew"
)

const (
	cloudWatchMetricsNamespace = "Buildkite"
)

// cloudWatchMetricsPublisher sends queue metrics to AWS CloudWatch
type cloudWatchMetricsPublisher struct {
	OrgSlug string
	Queue   string
}

// Publish queue metrics to CloudWatch Metrics
func (cp *cloudWatchMetricsPublisher) Publish(metrics map[string]int64) error {
	svc := cloudwatch.New(session.New())

	datum := []*cloudwatch.MetricDatum{}

	for k, v := range metrics {
		log.Printf("Publishing metric %s=%d [queue=%s,org=%s]",
			k, v, cp.OrgSlug, cp.Queue)

		datum = append(datum, &cloudwatch.MetricDatum{
			MetricName: aws.String(k),
			Unit:       aws.String("Count"),
			Value:      aws.Float64(float64(v)),
			Dimensions: []*cloudwatch.Dimension{
				&cloudwatch.Dimension{
					Name:  aws.String("Org"),
					Value: aws.String(cp.OrgSlug),
				},
				&cloudwatch.Dimension{
					Name:  aws.String("Queue"),
					Value: aws.String(cp.Queue),
				},
			},
		})
	}

	spew.Dump(datum)

	_, err := svc.PutMetricData(&cloudwatch.PutMetricDataInput{
		Namespace:  aws.String(cloudWatchMetricsNamespace),
		MetricData: datum,
	})

	return err
}

type dryRunMetricsPublisher struct {
}

func (p *dryRunMetricsPublisher) Publish(metrics map[string]int64) error {
	for k, v := range metrics {
		log.Printf("Publishing metric %s=%d", k, v)
	}
	return nil
}
