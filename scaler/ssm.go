package scaler

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
)

func RetrieveFromParameterStore(cfg aws.Config, key string) (string, error) {
	ssmClient := ssm.NewFromConfig(cfg)
	output, err := ssmClient.GetParameter(context.TODO(), &ssm.GetParameterInput{
		Name:           &key,
		WithDecryption: aws.Bool(true),
	})
	if err != nil {
		return "", err
	}

	return *output.Parameter.Value, nil
}
