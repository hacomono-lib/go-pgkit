package rdsiam

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// BeforeConnect returns a function for conn.WithBeforeConnect that sets a token from ts
// for cc.Host, cc.Port and cc.User as the password. A password in cc (e.g., from DB_PASSWORD) is overwritten.
// It applies only to the sessions it is passed to, so other sessions keep connecting with their password.
func BeforeConnect(ts *TokenSource) func(ctx context.Context, cc *pgx.ConnConfig) error {
	return func(ctx context.Context, cc *pgx.ConnConfig) error {
		token, err := ts.Token(ctx, cc.Host, cc.Port, cc.User)
		if err != nil {
			return err
		}
		cc.Password = token
		return nil
	}
}
