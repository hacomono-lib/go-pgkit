package rdsiam

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBeforeConnect(t *testing.T) {
	tests := []struct {
		name         string
		host         string
		password     string
		wantEndpoint string
	}{
		{
			name:         "sets the token as the password",
			host:         "db.example.com",
			wantEndpoint: "db.example.com:5432",
		},
		// A password read from the environment (e.g., DB_PASSWORD) must not be used for IAM database authentication.
		{
			name:         "overwrites the password",
			host:         "db.example.com",
			password:     "password",
			wantEndpoint: "db.example.com:5432",
		},
		{
			name:         "brackets an IPv6 host",
			host:         "::1",
			wantEndpoint: "[::1]:5432",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeBuilder{}
			ts, err := NewTokenSource(aws.Config{Region: "ap-northeast-1", Credentials: staticCreds(aws.Credentials{})})
			require.NoError(t, err)
			ts.build = fake.build
			cc := &pgx.ConnConfig{}
			cc.Host, cc.Port, cc.User, cc.Password = tt.host, 5432, "app_user", tt.password

			require.NoError(t, BeforeConnect(ts)(context.Background(), cc))

			require.Len(t, fake.built, 1)
			assert.Equal(t, fake.built[0].value, cc.Password)
			assert.Equal(t, tt.wantEndpoint, fake.built[0].endpoint)
			assert.Equal(t, "app_user", fake.built[0].dbUser)
		})
	}
}
