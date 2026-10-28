// Package rdsiam connects to Amazon RDS / Aurora PostgreSQL with IAM database authentication
// by setting an IAM auth token as the password before each new connection.
//
// ref: https://docs.aws.amazon.com/AmazonRDS/latest/AuroraUserGuide/UsingWithRDS.IAMDBAuth.html
package rdsiam

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/rds/auth"
	"golang.org/x/sync/singleflight"
)

const (
	// tokenValidity is how long a token is valid (X-Amz-Expires=900 in auth.BuildAuthToken).
	tokenValidity = 15 * time.Minute
	// refreshMargin is how long before a token becomes unusable it is rebuilt,
	// leaving time to open the connection.
	refreshMargin = time.Minute
	// tokenLifetime is how long a token is reused.
	tokenLifetime = tokenValidity - refreshMargin
)

// ErrInvalidConfig is returned by NewTokenSource when the AWS config has no region or credentials.
var ErrInvalidConfig = errors.New("rdsiam: invalid aws config")

type token struct {
	value     string
	refreshAt time.Time
}

// TokenSource returns IAM auth tokens, reusing them for each endpoint and user until they need to be rebuilt.
// Use BeforeConnect to set the tokens as the password of new connections.
type TokenSource struct {
	region string
	creds  aws.CredentialsProvider
	// build is auth.BuildAuthToken; tests replace it.
	build func(ctx context.Context, endpoint, region, dbUser string, creds aws.CredentialsProvider, optFns ...func(*auth.BuildAuthTokenOptions)) (string, error)
	now   func() time.Time

	mu     sync.Mutex
	tokens map[string]token
	group  singleflight.Group
}

// NewTokenSource creates a TokenSource that signs tokens with the region and credentials of cfg
// (e.g., from config.LoadDefaultConfig, which uses EKS Pod Identity on EKS).
func NewTokenSource(cfg aws.Config) (*TokenSource, error) {
	if cfg.Region == "" {
		return nil, fmt.Errorf("%w: region is empty", ErrInvalidConfig)
	}
	if cfg.Credentials == nil {
		return nil, fmt.Errorf("%w: credentials are nil", ErrInvalidConfig)
	}
	return &TokenSource{
		region: cfg.Region,
		creds:  cfg.Credentials,
		build:  auth.BuildAuthToken,
		now:    time.Now,
		tokens: map[string]token{},
	}, nil
}

// Token returns an IAM auth token for the DB user on host:port.
// A token is reused and rebuilt refreshMargin before it becomes unusable: the earlier of
// tokenValidity after signing and the expiry of the signing credentials.
func (t *TokenSource) Token(ctx context.Context, host string, port uint16, user string) (string, error) {
	endpoint := net.JoinHostPort(host, strconv.Itoa(int(port)))
	key := user + "@" + endpoint
	t.mu.Lock()
	tok, ok := t.tokens[key]
	t.mu.Unlock()
	if ok && t.now().Before(tok.refreshAt) {
		return tok.value, nil
	}

	// Concurrent calls for the same key share one build. It runs without the caller's cancellation,
	// so that one caller giving up does not fail the others.
	ch := t.group.DoChan(key, func() (any, error) {
		return t.rebuild(context.WithoutCancel(ctx), endpoint, user, key)
	})
	select {
	case res := <-ch:
		if res.Err != nil {
			return "", res.Err
		}
		return res.Val.(string), nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func (t *TokenSource) rebuild(ctx context.Context, endpoint, user, key string) (string, error) {
	creds, err := t.creds.Retrieve(ctx)
	if err != nil {
		return "", fmt.Errorf("rdsiam: retrieve credentials: %w", err)
	}
	value, err := t.build(ctx, endpoint, t.region, user, aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
		return creds, nil
	}))
	if err != nil {
		return "", fmt.Errorf("rdsiam: build auth token: %w", err)
	}

	refreshAt := t.now().Add(tokenLifetime)
	if credsRefreshAt := creds.Expires.Add(-refreshMargin); creds.CanExpire && credsRefreshAt.Before(refreshAt) {
		refreshAt = credsRefreshAt
	}
	t.mu.Lock()
	t.tokens[key] = token{value: value, refreshAt: refreshAt}
	t.mu.Unlock()
	return value, nil
}
