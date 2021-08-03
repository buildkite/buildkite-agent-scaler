package scaler

import (
	"fmt"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/secretsmanager"
	"github.com/aws/aws-sdk-go/service/ssm"
)

func RetrieveFromParameterStore(sess *session.Session, key string) (string, error) {
	ssmClient := ssm.New(sess)
	output, err := ssmClient.GetParameter(&ssm.GetParameterInput{
		Name:           &key,
		WithDecryption: aws.Bool(true),
	})
	if err != nil {
		return "", err
	}

	return *output.Parameter.Value, nil
}

func RetrieveFromSecretsManager(sess *session.Session, secretID string) (string, error) {
	svc := secretsmanager.New(sess)
	input := &secretsmanager.GetSecretValueInput{
		SecretId: aws.String(secretID),
	}

	result, err := svc.GetSecretValue(input)
	if err != nil {
		return "", err
	}

	if result.SecretString == nil {
		return "", fmt.Errorf("kms encrypted values not supported")
	}

	return *result.SecretString, nil
}
