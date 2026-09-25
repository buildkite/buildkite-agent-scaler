package scaler

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
)

func RetrieveFromParameterStore(cfg aws.Config, key, region string) (string, error) {
	ssmClient := newSSMClient(cfg, region)
	output, err := ssmClient.GetParameter(context.TODO(), &ssm.GetParameterInput{
		Name:           aws.String(key),
		WithDecryption: aws.Bool(true),
	})
	if err != nil {
		return "", err
	}
	return *output.Parameter.Value, nil
}

func newSSMClient(cfg aws.Config, region string) *ssm.Client {
	if region == "" {
		return ssm.NewFromConfig(cfg)
	}
	return ssm.NewFromConfig(cfg, func(o *ssm.Options) {
		o.Region = region
	})
}
