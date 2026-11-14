package processor

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"testing"
	"time"

	"github.com/l3uddz/autoscan"
)

var (
	errFakeInsert   = errors.New("insert failed")
	errFakeRollback = errors.New("rollback failed")
)

// failingConnector opens connections on which every statement fails.
// Rollback returns rollbackErr.
type failingConnector struct{ rollbackErr error }

func (c failingConnector) Connect(context.Context) (driver.Conn, error) {
	return failingConn(c), nil
}

func (c failingConnector) Driver() driver.Driver { return failingDriver(c) }

type failingDriver failingConnector

func (d failingDriver) Open(string) (driver.Conn, error) { return failingConn(d), nil }

type failingConn failingConnector

func (failingConn) Prepare(string) (driver.Stmt, error) { return nil, errFakeInsert }
func (failingConn) Close() error                        { return nil }
func (c failingConn) Begin() (driver.Tx, error)         { return failingTx(c), nil }

type failingTx failingConnector

func (failingTx) Commit() error     { return nil }
func (t failingTx) Rollback() error { return t.rollbackErr }

func TestUpsertInsertFailure(t *testing.T) {
	type Test struct {
		Name         string
		RollbackErr  error
		WantRollback bool
	}

	var testCases = []Test{
		{
			Name: "Returns the insert error when the rollback succeeds",
		},
		{
			Name:         "Returns the insert and rollback errors when the rollback fails",
			RollbackErr:  errFakeRollback,
			WantRollback: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			db := sql.OpenDB(failingConnector{rollbackErr: tc.RollbackErr})
			defer db.Close()

			store := &datastore{db: db}
			scans := []autoscan.Scan{{Folder: "/mnt/tv", Time: time.Now()}}

			err := store.Upsert(scans)
			if !errors.Is(err, errFakeInsert) {
				t.Errorf("Got error %v, want the insert error", err)
			}

			if got := errors.Is(err, errFakeRollback); got != tc.WantRollback {
				t.Errorf("Got error %v, want the rollback error: %v", err, tc.WantRollback)
			}
		})
	}
}
