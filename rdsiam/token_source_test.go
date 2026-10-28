package rdsiam

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/rds/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func staticCreds(c aws.Credentials) aws.CredentialsProvider {
	return aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) { return c, nil })
}

// fakeBuilder replaces auth.BuildAuthToken and records the tokens it built.
type fakeBuilder struct {
	built []builtToken
}

type builtToken struct {
	value    string
	endpoint string
	dbUser   string
}

func (f *fakeBuilder) build(ctx context.Context, endpoint, region, dbUser string, creds aws.CredentialsProvider, optFns ...func(*auth.BuildAuthTokenOptions)) (string, error) {
	tok := builtToken{value: fmt.Sprintf("token-%d", len(f.built)+1), endpoint: endpoint, dbUser: dbUser}
	f.built = append(f.built, tok)
	return tok.value, nil
}

// latest returns the token built last for dbUser@endpoint.
func (f *fakeBuilder) latest(dbUser, endpoint string) string {
	for i := len(f.built) - 1; i >= 0; i-- {
		if f.built[i].dbUser == dbUser && f.built[i].endpoint == endpoint {
			return f.built[i].value
		}
	}
	return ""
}

func TestNewTokenSource(t *testing.T) {
	tests := []struct {
		name string
		cfg  aws.Config
	}{
		{
			name: "no region",
			cfg:  aws.Config{Credentials: staticCreds(aws.Credentials{})},
		},
		{
			name: "no credentials",
			cfg:  aws.Config{Region: "ap-northeast-1"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewTokenSource(tt.cfg)
			assert.ErrorIs(t, err, ErrInvalidConfig)
		})
	}
}

func TestToken(t *testing.T) {
	start := time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC)
	type call struct {
		// elapsed is the time since the first call
		elapsed time.Duration
		host    string
		user    string
		// wantBuild is whether the call builds a new token; otherwise it reuses one
		wantBuild bool
	}
	tests := []struct {
		name string
		// credsExpiresIn of 0 means credentials that do not expire
		credsExpiresIn time.Duration
		calls          []call
	}{
		{
			name: "reuses the token until tokenLifetime",
			calls: []call{
				{elapsed: 0, host: "db.example.com", user: "app_user", wantBuild: true},
				{elapsed: tokenLifetime - time.Second, host: "db.example.com", user: "app_user"},
			},
		},
		{
			name: "rebuilds the token after tokenLifetime",
			calls: []call{
				{elapsed: 0, host: "db.example.com", user: "app_user", wantBuild: true},
				{elapsed: tokenLifetime, host: "db.example.com", user: "app_user", wantBuild: true},
			},
		},
		{
			name:           "reuses the token until refreshMargin before the credentials expire",
			credsExpiresIn: 6 * time.Minute,
			calls: []call{
				{elapsed: 0, host: "db.example.com", user: "app_user", wantBuild: true},
				{elapsed: 6*time.Minute - refreshMargin - time.Second, host: "db.example.com", user: "app_user"},
			},
		},
		{
			name:           "rebuilds the token refreshMargin before the credentials expire",
			credsExpiresIn: 6 * time.Minute,
			calls: []call{
				{elapsed: 0, host: "db.example.com", user: "app_user", wantBuild: true},
				{elapsed: 6*time.Minute - refreshMargin, host: "db.example.com", user: "app_user", wantBuild: true},
			},
		},
		{
			name: "keeps a token for each host and user",
			calls: []call{
				{elapsed: 0, host: "db.example.com", user: "app_user", wantBuild: true},
				{elapsed: 0, host: "db.example.com", user: "reader_user", wantBuild: true},
				{elapsed: 0, host: "db-ro.example.com", user: "app_user", wantBuild: true},
				{elapsed: 0, host: "db-ro.example.com", user: "reader_user", wantBuild: true},
				{elapsed: 0, host: "db.example.com", user: "app_user"},
				{elapsed: 0, host: "db.example.com", user: "reader_user"},
				{elapsed: 0, host: "db-ro.example.com", user: "app_user"},
				{elapsed: 0, host: "db-ro.example.com", user: "reader_user"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			now := start
			fake := &fakeBuilder{}
			ts, err := NewTokenSource(aws.Config{
				Region:      "ap-northeast-1",
				Credentials: staticCreds(aws.Credentials{CanExpire: tt.credsExpiresIn > 0, Expires: start.Add(tt.credsExpiresIn)}),
			})
			require.NoError(t, err)
			ts.build = fake.build
			ts.now = func() time.Time { return now }

			for i, c := range tt.calls {
				now = start.Add(c.elapsed)
				builtBefore := len(fake.built)
				token, err := ts.Token(context.Background(), c.host, 5432, c.user)
				require.NoError(t, err)

				endpoint := net.JoinHostPort(c.host, "5432")
				wantNewBuilds := 0
				if c.wantBuild {
					wantNewBuilds = 1
				}
				assert.Equal(t, wantNewBuilds, len(fake.built)-builtBefore, "call %d: tokens built", i)
				assert.Equal(t, fake.latest(c.user, endpoint), token, "call %d should return the latest token built for its user and endpoint", i)
			}
		})
	}
}
