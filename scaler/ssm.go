package scaler

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

func RetrieveFromParameterStore(cfg aws.Config, key string) (string, error) {
	ssmClient := ssm.NewFromConfig(cfg)
	output, err := ssmClient.GetParameter(context.TODO(), &ssm.GetParameterInput{
		Name:           aws.String(key),
		WithDecryption: aws.Bool(true),
	})
	if err != nil {
		return "", err
	}
	return *output.Parameter.Value, nil
}

// errAnotherScaleIn is returned by Save when another container saved a
// scale-in between this container's Load and Save. The caller lost the race
// and must not scale in.
var errAnotherScaleIn = errors.New("another container started a scale-in first")

// lastScaleInStore shares the time of the last scale-in between Lambda
// containers, so the cooldown survives recycling and holds across containers
// running at once. The ASG activity history can't provide this: in Elastic CI
// Mode agents leave the group themselves after draining, and their "instance
// terminated" activity looks the same whether the scaler asked them to or not.
type lastScaleInStore interface {
	// Load returns the stored time, initializing a missing parameter with
	// the current time so upgrades wait out any previous cooldown.
	Load(ctx context.Context) (time.Time, error)
	// Save stores t. It returns errAnotherScaleIn if the store changed since
	// Load, so of two containers racing to scale in, only one proceeds.
	Save(ctx context.Context, t time.Time) error
}

// lastScaleInParameterAPI is the subset of ssm.Client used by the store.
type lastScaleInParameterAPI interface {
	GetParameter(ctx context.Context, params *ssm.GetParameterInput, optFns ...func(*ssm.Options)) (*ssm.GetParameterOutput, error)
	PutParameter(ctx context.Context, params *ssm.PutParameterInput, optFns ...func(*ssm.Options)) (*ssm.PutParameterOutput, error)
}

// ssmLastScaleInStore keeps the last scale-in time in a String parameter in
// SSM Parameter Store, formatted as RFC 3339.
//
// Parameter Store has no conditional write, but every PutParameter bumps the
// parameter's version by exactly one and returns the new number. Save expects
// the version after its own write to be one more than the version Load saw;
// anything else means another container wrote in between.
type ssmLastScaleInStore struct {
	client lastScaleInParameterAPI
	name   string

	// version is the parameter version Load saw, or 0 when the parameter
	// didn't exist yet.
	version int64
}

func (s *ssmLastScaleInStore) Load(ctx context.Context) (time.Time, error) {
	output, err := s.client.GetParameter(ctx, &ssm.GetParameterInput{
		Name: aws.String(s.name),
	})
	var notFound *types.ParameterNotFound
	if errors.As(err, &notFound) {
		// Don't overwrite a timestamp another container created after our
		// read. If creation fails (including a race), the next poll reloads.
		now := time.Now().UTC().Truncate(time.Second)
		created, err := s.client.PutParameter(ctx, &ssm.PutParameterInput{
			Name:      aws.String(s.name),
			Value:     aws.String(now.Format(time.RFC3339)),
			Type:      types.ParameterTypeString,
			Overwrite: aws.Bool(false),
		})
		if err != nil {
			return time.Time{}, fmt.Errorf("initialize parameter %s: %w", s.name, err)
		}
		s.version = created.Version
		return now, nil
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("get parameter %s: %w", s.name, err)
	}

	value := aws.ToString(output.Parameter.Value)
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse parameter %s value %q: %w", s.name, value, err)
	}
	s.version = output.Parameter.Version
	return t, nil
}

func (s *ssmLastScaleInStore) Save(ctx context.Context, t time.Time) error {
	output, err := s.client.PutParameter(ctx, &ssm.PutParameterInput{
		Name:      aws.String(s.name),
		Value:     aws.String(t.UTC().Format(time.RFC3339)),
		Type:      types.ParameterTypeString,
		Overwrite: aws.Bool(true),
	})
	if err != nil {
		return fmt.Errorf("put parameter %s: %w", s.name, err)
	}
	if output.Version != s.version+1 {
		return fmt.Errorf("parameter %s is at version %d, expected %d: %w", s.name, output.Version, s.version+1, errAnotherScaleIn)
	}
	s.version = output.Version
	return nil
}
