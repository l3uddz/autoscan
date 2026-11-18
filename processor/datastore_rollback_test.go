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

// txCalls records which transaction methods ran.
type txCalls struct {
	committed  bool
	rolledBack bool
}

// failingConnector opens connections on which every statement fails.
// Rollback returns rollbackErr. Both connections and transactions record
// their calls in calls.
type failingConnector struct {
	rollbackErr error
	calls       *txCalls
}

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

func (t failingTx) Commit() error {
	t.calls.committed = true
	return nil
}

func (t failingTx) Rollback() error {
	t.calls.rolledBack = true
	return t.rollbackErr
}

func TestUpsertInsertFailure(t *testing.T) {
	type Test struct {
		Name        string
		RollbackErr error
	}

	var testCases = []Test{
		{
			Name: "Returns the insert error when the rollback succeeds",
		},
		{
			Name:        "Returns the insert error when the rollback fails",
			RollbackErr: errFakeRollback,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			calls := &txCalls{}
			db := sql.OpenDB(failingConnector{rollbackErr: tc.RollbackErr, calls: calls})
			defer db.Close()

			store := &datastore{db: db}
			scans := []autoscan.Scan{{Folder: "/mnt/tv", Time: time.Now()}}

			err := store.Upsert(scans)
			if !errors.Is(err, errFakeInsert) {
				t.Errorf("Got error %v, want the insert error", err)
			}

			if calls.committed {
				t.Error("Upsert committed the transaction after the insert failed")
			}

			if !calls.rolledBack {
				t.Error("Upsert did not roll back the transaction after the insert failed")
			}
		})
	}
}
