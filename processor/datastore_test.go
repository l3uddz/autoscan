package processor

import (
	"database/sql"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/l3uddz/autoscan"
	"github.com/l3uddz/autoscan/migrate"

	// sqlite3 driver
	_ "modernc.org/sqlite"
)

const sqlGetScan = `
SELECT folder, priority, time FROM scan
WHERE folder = ?
`

func (store *datastore) GetScan(folder string) (autoscan.Scan, error) {
	row := store.db.QueryRow(sqlGetScan, folder)

	scan := autoscan.Scan{}
	err := row.Scan(&scan.Folder, &scan.Priority, &scan.Time)

	return scan, err
}

func getDatastore(t *testing.T) *datastore {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}

	mg, err := migrate.New(db, "migrations")
	if err != nil {
		t.Fatal(err)
	}

	ds, err := newDatastore(db, mg)
	if err != nil {
		t.Fatal(err)
	}

	return ds
}

func TestUpsert(t *testing.T) {
	type Test struct {
		Name     string
		Scans    []autoscan.Scan
		WantScan autoscan.Scan
	}

	var testCases = []Test{
		{
			Name: "All fields",
			Scans: []autoscan.Scan{
				{
					Folder:   "testfolder/test",
					Priority: 5,
					Time:     time.Time{}.Add(1),
				},
			},
			WantScan: autoscan.Scan{
				Folder:   "testfolder/test",
				Priority: 5,
				Time:     time.Time{}.Add(1),
			},
		},
		{
			Name: "Priority shall increase but not decrease",
			Scans: []autoscan.Scan{
				{
					Priority: 2,
					Time:     time.Time{}.Add(1),
				},
				{
					Priority: 5,
					Time:     time.Time{}.Add(2),
				},
				{
					Priority: 3,
					Time:     time.Time{}.Add(3),
				},
			},
			WantScan: autoscan.Scan{
				Priority: 5,
				Time:     time.Time{}.Add(3),
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			store := getDatastore(t)
			err := store.Upsert(tc.Scans)
			if err != nil {
				t.Fatal(err)
			}

			scan, err := store.GetScan(tc.WantScan.Folder)
			if err != nil {
				t.Fatal(err)
			}

			if !reflect.DeepEqual(tc.WantScan, scan) {
				t.Log(scan)
				t.Errorf("Scans do not equal")
			}
		})
	}
}

func TestGetAvailableScan(t *testing.T) {
	type Test struct {
		Name      string
		Now       time.Time
		MinAge    time.Duration
		GiveScans []autoscan.Scan
		WantErr   error
		WantScan  autoscan.Scan
	}

	testTime := time.Now().UTC()

	var testCases = []Test{
		{
			Name:   "Retrieves no folders if all folders are too young",
			Now:    testTime,
			MinAge: 2 * time.Minute,
			GiveScans: []autoscan.Scan{
				{Folder: "1", Time: testTime.Add(-1 * time.Minute)},
				{Folder: "2", Time: testTime.Add(-1 * time.Minute)},
			},
			WantErr: autoscan.ErrNoScans,
		},
		{
			Name:   "Retrieves folder if older than minimum age",
			Now:    testTime,
			MinAge: 5 * time.Minute,
			GiveScans: []autoscan.Scan{
				{Folder: "1", Time: testTime.Add(-6 * time.Minute)},
			},
			WantScan: autoscan.Scan{
				Folder: "1", Time: testTime.Add(-6 * time.Minute),
			},
		},
		{
			Name:   "Returns all fields",
			Now:    testTime,
			MinAge: 5 * time.Minute,
			GiveScans: []autoscan.Scan{
				{
					Folder:   "Amazing folder",
					Priority: 69,
					Time:     testTime.Add(-6 * time.Minute),
				},
			},
			WantScan: autoscan.Scan{
				Folder:   "Amazing folder",
				Priority: 69,
				Time:     testTime.Add(-6 * time.Minute),
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			store := getDatastore(t)
			err := store.Upsert(tc.GiveScans)
			if err != nil {
				t.Fatal(err)
			}

			now = func() time.Time {
				return tc.Now
			}

			scan, err := store.GetAvailableScan(tc.MinAge)
			if !errors.Is(err, tc.WantErr) {
				t.Fatal(err)
			}

			if !reflect.DeepEqual(scan.Scan, tc.WantScan) {
				t.Log(scan)
				t.Log(tc.WantScan)
				t.Errorf("Scan does not match")
			}
		})
	}
}

func TestDelete(t *testing.T) {
	type Test struct {
		Name      string
		GiveScans []autoscan.Scan
		WantScans []autoscan.Scan
	}

	var testCases = []Test{
		{
			Name: "Only deletes specific folder, not other folders",
			GiveScans: []autoscan.Scan{
				{Folder: "1", Priority: 1},
				{Folder: "2"},
			},
			WantScans: []autoscan.Scan{
				{Folder: "2"},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			store := getDatastore(t)
			err := store.Upsert(tc.GiveScans)
			if err != nil {
				t.Fatal(err)
			}

			scan, err := store.GetAvailableScan(0)
			if err != nil {
				t.Fatal(err)
			}
			err = store.Delete(scan)
			if err != nil {
				t.Fatal(err)
			}

			scans, err := store.GetAll()
			if err != nil {
				t.Fatal(err)
			}

			if !reflect.DeepEqual(scans, tc.WantScans) {
				t.Log(scans)
				t.Errorf("Scans do not match")
			}
		})
	}
}

func TestScanRevisionMigrationKeepsPendingScans(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	mg, err := migrate.New(db, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	schema, err := migrations.ReadFile("migrations/1_init.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(schema)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO schema_migration (component, version) VALUES ('processor', 1)`); err != nil {
		t.Fatal(err)
	}
	first := autoscan.Scan{Folder: "/mnt/media/show", Priority: 5, Time: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)}
	other := autoscan.Scan{Folder: "/mnt/media/movie", Priority: 2, Time: first.Time.Add(-time.Hour)}
	if _, err := db.Exec(`INSERT INTO scan (folder, priority, time) VALUES (?, ?, ?), (?, ?, ?)`,
		first.Folder, first.Priority, first.Time, other.Folder, other.Priority, other.Time); err != nil {
		t.Fatal(err)
	}

	store, err := newDatastore(db, mg)
	if err != nil {
		t.Fatal(err)
	}
	scan, err := store.GetAvailableScan(0)
	if err != nil || scan.Scan != first {
		t.Fatalf("first scan after upgrade = %+v, %v, want %+v, nil", scan.Scan, err, first)
	}
	if err := store.Delete(scan); err != nil {
		t.Fatal(err)
	}
	remaining, err := store.GetAll()
	if err != nil || len(remaining) != 1 || remaining[0] != other {
		t.Errorf("remaining scans after deletion = %+v, %v, want [%+v], nil", remaining, err, other)
	}
}
