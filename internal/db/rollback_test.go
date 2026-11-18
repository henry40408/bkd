package db

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func TestAConnectionThatRolledBackIsReturnedToThePool(t *testing.T) {
	pool := openTemp(t)
	err := WithImmediateTx(context.Background(), pool, func(*sql.Conn) error { return errors.New("boom") })
	if err == nil {
		t.Fatal("the failure was swallowed")
	}
	if pool.Stats().OpenConnections == 0 {
		t.Fatal("a healthy connection was thrown away after a rollback")
	}
}
