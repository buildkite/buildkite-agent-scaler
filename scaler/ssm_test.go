package scaler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

type fakeLastScaleInParameterAPI struct {
	getOutput  *ssm.GetParameterOutput
	getErr     error
	putInput   *ssm.PutParameterInput
	putVersion int64
	putErr     error
}

func (f *fakeLastScaleInParameterAPI) GetParameter(context.Context, *ssm.GetParameterInput, ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
	return f.getOutput, f.getErr
}

func (f *fakeLastScaleInParameterAPI) PutParameter(_ context.Context, in *ssm.PutParameterInput, _ ...func(*ssm.Options)) (*ssm.PutParameterOutput, error) {
	f.putInput = in
	return &ssm.PutParameterOutput{Version: f.putVersion}, f.putErr
}

func TestSSMLastScaleInStoreLoad(t *testing.T) {
	storedAt := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)

	testCases := []struct {
		name        string
		getOutput   *ssm.GetParameterOutput
		getErr      error
		want        time.Time
		wantVersion int64
		wantErr     bool
	}{
		{
			name: "parses the stored RFC 3339 time",
			getOutput: &ssm.GetParameterOutput{
				Parameter: &types.Parameter{Value: aws.String(storedAt.Format(time.RFC3339)), Version: 7},
			},
			want:        storedAt,
			wantVersion: 7,
		},
		{
			name:    "other API errors propagate",
			getErr:  errors.New("access denied"),
			wantErr: true,
		},
		{
			name: "garbage values are errors",
			getOutput: &ssm.GetParameterOutput{
				Parameter: &types.Parameter{Value: aws.String("yesterday")},
			},
			wantErr: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			api := &fakeLastScaleInParameterAPI{getOutput: tc.getOutput, getErr: tc.getErr}
			store := &ssmLastScaleInStore{
				client: api,
				name:   "/scaler/last-scale-in",
			}

			got, err := store.Load(t.Context())
			if api.putInput != nil {
				t.Error("Load must not overwrite an existing timestamp or mask a read error")
			}
			if (err != nil) != tc.wantErr {
				t.Fatalf("Load() error = %v, wantErr %t", err, tc.wantErr)
			}
			if !got.Equal(tc.want) {
				t.Errorf("Load() = %v, want %v", got, tc.want)
			}
			if tc.wantErr {
				return
			}
			if store.version != tc.wantVersion {
				t.Errorf("version = %d, want %d", store.version, tc.wantVersion)
			}
		})
	}
}

func TestSSMLastScaleInStoreInitializesMissingParameter(t *testing.T) {
	for name, putErr := range map[string]error{"created": nil, "another container created it first": &types.ParameterAlreadyExists{}} {
		t.Run(name, func(t *testing.T) {
			api := &fakeLastScaleInParameterAPI{
				getErr: &types.ParameterNotFound{}, putVersion: 1, putErr: putErr,
			}
			store := &ssmLastScaleInStore{client: api, name: "/scaler/last-scale-in"}
			before := time.Now().UTC().Truncate(time.Second)
			got, err := store.Load(t.Context())
			if !errors.Is(err, putErr) {
				t.Fatalf("Load() error = %v, want %v", err, putErr)
			}
			if api.putInput == nil {
				t.Fatal("missing parameter was not initialized")
			}
			if aws.ToBool(api.putInput.Overwrite) {
				t.Error("initialization must not overwrite another container's timestamp")
			}
			if putErr != nil {
				return
			}
			if got.Before(before) || got.After(time.Now()) || store.version != 1 {
				t.Fatalf("Load() = %v, version %d, want current time and version 1", got, store.version)
			}
			if aws.ToString(api.putInput.Value) != got.Format(time.RFC3339) {
				t.Errorf("persisted %q, want %s", aws.ToString(api.putInput.Value), got.Format(time.RFC3339))
			}
		})
	}
}

// TestSSMLastScaleInStoreSaveDetectsRace pins the version check: a write
// that lands exactly one version after the one Load saw is ours alone; any
// other version means another container wrote in between.
func TestSSMLastScaleInStoreSaveDetectsRace(t *testing.T) {
	testCases := []struct {
		name       string
		loaded     int64
		putVersion int64
		putErr     error
		wantRaced  bool
		wantErr    bool
	}{
		{name: "first write creates version 1", loaded: 0, putVersion: 1},
		{name: "next version is ours", loaded: 7, putVersion: 8},
		{name: "skipped version means another container wrote first", loaded: 7, putVersion: 9, wantRaced: true, wantErr: true},
		{name: "API errors propagate", loaded: 7, putErr: errors.New("access denied"), wantErr: true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			store := &ssmLastScaleInStore{
				client:  &fakeLastScaleInParameterAPI{putVersion: tc.putVersion, putErr: tc.putErr},
				name:    "/scaler/last-scale-in",
				version: tc.loaded,
			}

			err := store.Save(t.Context(), time.Now())
			if (err != nil) != tc.wantErr {
				t.Fatalf("Save() error = %v, wantErr %t", err, tc.wantErr)
			}
			if got := errors.Is(err, errAnotherScaleIn); got != tc.wantRaced {
				t.Fatalf("Save() error = %v, want errAnotherScaleIn %t", err, tc.wantRaced)
			}
			if tc.wantErr {
				return
			}
			if store.version != tc.putVersion {
				t.Errorf("version = %d, want %d", store.version, tc.putVersion)
			}
		})
	}
}

func TestSSMLastScaleInStoreSave(t *testing.T) {
	sydney := time.FixedZone("AEST", 10*60*60)
	at := time.Date(2026, 9, 4, 22, 0, 0, 0, sydney)

	api := &fakeLastScaleInParameterAPI{putVersion: 1}
	store := &ssmLastScaleInStore{client: api, name: "/scaler/last-scale-in"}

	if err := store.Save(t.Context(), at); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	in := api.putInput
	if in == nil {
		t.Fatal("PutParameter not called")
	}
	if got := aws.ToString(in.Name); got != "/scaler/last-scale-in" {
		t.Errorf("Name = %q, want %q", got, "/scaler/last-scale-in")
	}
	if got, want := aws.ToString(in.Value), "2026-09-04T12:00:00Z"; got != want {
		t.Errorf("Value = %q, want %q", got, want)
	}
	if in.Type != types.ParameterTypeString {
		t.Errorf("Type = %q, want %q", in.Type, types.ParameterTypeString)
	}
	if !aws.ToBool(in.Overwrite) {
		t.Error("Overwrite = false, want true")
	}
}
