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

func pendingScans(store *datastore) (scans []autoscan.Scan, err error) {
	rows, err := store.db.Query(`SELECT folder, priority, time FROM scan`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var scan autoscan.Scan
		if err := rows.Scan(&scan.Folder, &scan.Priority, &scan.Time); err != nil {
			return nil, err
		}
		scans = append(scans, scan)
	}
	return scans, rows.Err()
}

func getDatastore(t *testing.T) *datastore {
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

	ds, err := newDatastore(db, mg)
	if err != nil {
		t.Fatal(err)
	}

	if err := ds.configureTargets([]Target{{ID: "target"}}); err != nil {
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
		{
			Name: "Time shall advance but not move backwards",
			Scans: []autoscan.Scan{
				{
					Folder:   "testfolder/show",
					Priority: 2,
					Time:     time.Date(2026, 10, 25, 1, 0, 0, 500_000_000, time.UTC),
				},
				{
					Folder:   "testfolder/show",
					Priority: 5,
					Time:     time.Date(2026, 10, 25, 1, 0, 0, 0, time.UTC),
				},
			},
			WantScan: autoscan.Scan{
				Folder:   "testfolder/show",
				Priority: 5,
				Time:     time.Date(2026, 10, 25, 1, 0, 0, 500_000_000, time.UTC),
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
			WantErr: errNoScans,
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

			scan, err := store.GetAvailableScan(t.Context(), "target", tc.Now.Add(-tc.MinAge))
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

func TestCompleteDelivery(t *testing.T) {
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

			scan, err := store.GetAvailableScan(t.Context(), "target", time.Now())
			if err != nil {
				t.Fatal(err)
			}
			_, err = store.completeDelivery("target", scan)
			if err != nil {
				t.Fatal(err)
			}

			scans, err := pendingScans(store)
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
	if err := store.configureTargets([]Target{{ID: "target"}, {ID: "other target"}}); err != nil {
		t.Fatal(err)
	}

	scan, err := store.GetAvailableScan(t.Context(), "target", time.Now())
	if err != nil || scan.Scan != first {
		t.Fatalf("first scan after upgrade = %+v, %v, want %+v, nil", scan.Scan, err, first)
	}
	if _, err := store.completeDelivery("target", scan); err != nil {
		t.Fatal(err)
	}
	otherTargetScan, err := store.GetAvailableScan(t.Context(), "other target", time.Now())
	if err != nil || otherTargetScan.Scan != first {
		t.Fatalf("other target's first scan after upgrade = %+v, %v, want %+v, nil", otherTargetScan.Scan, err, first)
	}
	if _, err := store.completeDelivery("other target", otherTargetScan); err != nil {
		t.Fatal(err)
	}
	remaining, err := pendingScans(store)
	if err != nil || len(remaining) != 1 || remaining[0] != other {
		t.Errorf("remaining scans after deletion = %+v, %v, want [%+v], nil", remaining, err, other)
	}
}

func TestQueuedScansSurviveAStartupWithoutTargets(t *testing.T) {
	store := getDatastore(t)
	scan := autoscan.Scan{Folder: "queued", Time: time.Now().UTC().Add(-time.Hour)}
	if err := store.Upsert([]autoscan.Scan{scan}); err != nil {
		t.Fatal(err)
	}
	if err := store.configureTargets(nil); err != nil {
		t.Fatal(err)
	}
	if statuses, _, err := store.queueStatus(t.Context()); err != nil || len(statuses) != 0 {
		t.Fatalf("target statuses = %+v, %v, want no configured targets", statuses, err)
	}
	if err := store.configureTargets([]Target{{ID: "new target"}}); err != nil {
		t.Fatal(err)
	}
	queued, err := store.GetAvailableScan(t.Context(), "new target", time.Now())
	if err != nil || queued.Scan != scan {
		t.Fatalf("new target's pending scan = %+v, %v, want %+v, nil", queued.Scan, err, scan)
	}
}

func TestGetAvailableScanComparesTimesAcrossOffsets(t *testing.T) {
	plusOne := time.FixedZone("+01", 3600)
	minusOne := time.FixedZone("-01", -3600)

	testCases := []struct {
		Name       string
		GiveScans  []autoscan.Scan
		OlderThan  time.Time
		WantFolder string
	}{
		{
			Name:       "Returns a scan older than a cutoff with another offset",
			GiveScans:  []autoscan.Scan{{Folder: "old", Time: time.Date(2026, 10, 25, 1, 30, 0, 0, plusOne)}},
			OlderThan:  time.Date(2026, 10, 25, 1, 15, 0, 0, time.UTC),
			WantFolder: "old",
		},
		{
			Name:      "Keeps a scan younger than a cutoff with another offset",
			GiveScans: []autoscan.Scan{{Folder: "young", Time: time.Date(2026, 10, 25, 1, 10, 0, 0, minusOne)}},
			OlderThan: time.Date(2026, 10, 25, 1, 15, 0, 0, time.UTC),
		},
		{
			Name:       "Returns a UTC scan older than a cutoff in another zone",
			GiveScans:  []autoscan.Scan{{Folder: "old", Time: time.Date(2026, 10, 25, 1, 30, 0, 0, time.UTC)}},
			OlderThan:  time.Date(2026, 10, 25, 1, 15, 0, 0, minusOne),
			WantFolder: "old",
		},
		{
			Name: "Returns the oldest scan first across offsets",
			GiveScans: []autoscan.Scan{
				{Folder: "later", Time: time.Date(2026, 10, 25, 1, 20, 0, 0, time.UTC)},
				{Folder: "earlier", Time: time.Date(2026, 10, 25, 2, 10, 0, 0, plusOne)},
			},
			OlderThan:  time.Date(2026, 10, 25, 2, 0, 0, 0, time.UTC),
			WantFolder: "earlier",
		},
		{
			Name: "Returns a fractional second before the next whole second",
			GiveScans: []autoscan.Scan{
				{Folder: "whole", Time: time.Date(2026, 10, 25, 1, 0, 1, 0, time.UTC)},
				{Folder: "fraction", Time: time.Date(2026, 10, 25, 1, 0, 0, 500_000_000, time.UTC)},
			},
			OlderThan:  time.Date(2026, 10, 25, 2, 0, 0, 0, time.UTC),
			WantFolder: "fraction",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			store := getDatastore(t)
			if err := store.Upsert(tc.GiveScans); err != nil {
				t.Fatal(err)
			}

			scan, err := store.GetAvailableScan(t.Context(), "target", tc.OlderThan)
			if tc.WantFolder == "" {
				if !errors.Is(err, errNoScans) {
					t.Fatalf("available scan = %+v, %v, want %v", scan.Scan, err, errNoScans)
				}
				return
			}
			if err != nil || scan.Folder != tc.WantFolder {
				t.Fatalf("available scan = %+v, %v, want folder %q", scan.Scan, err, tc.WantFolder)
			}
		})
	}
}

func TestStartupStoresEarlierScanTimesInUTC(t *testing.T) {
	store := getDatastore(t)
	legacy := []struct {
		Folder   string
		Stored   string
		WantText string
		WantTime time.Time
	}{
		{"bst", "2026-10-25 01:30:00 +0100 BST", "2026-10-25 00:30:00 +0000 UTC", time.Date(2026, 10, 25, 0, 30, 0, 0, time.UTC)},
		{"monotonic", "2026-10-25 01:20:00.5 +0100 BST m=+0.002558546", "2026-10-25 00:20:00.5 +0000 UTC", time.Date(2026, 10, 25, 0, 20, 0, 500_000_000, time.UTC)},
		{"mattn", "2026-10-25 01:10:00+01:00", "2026-10-25 00:10:00 +0000 UTC", time.Date(2026, 10, 25, 0, 10, 0, 0, time.UTC)},
		{"utc", "2026-10-25 00:40:00 +0000 UTC", "2026-10-25 00:40:00 +0000 UTC", time.Date(2026, 10, 25, 0, 40, 0, 0, time.UTC)},
	}
	for _, row := range legacy {
		if _, err := store.db.Exec(`INSERT INTO scan (folder, priority, time) VALUES (?, 0, ?)`, row.Folder, row.Stored); err != nil {
			t.Fatal(err)
		}
		if _, err := store.db.Exec(`INSERT INTO scan_delivery (target, folder) VALUES ('target', ?)`, row.Folder); err != nil {
			t.Fatal(err)
		}
	}

	mg, err := migrate.New(store.db, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	if store, err = newDatastore(store.db, mg); err != nil {
		t.Fatal(err)
	}

	for _, row := range legacy {
		var text string
		if err := store.db.QueryRow(`SELECT CAST(time AS TEXT) FROM scan WHERE folder = ?`, row.Folder).Scan(&text); err != nil {
			t.Fatal(err)
		}
		if text != row.WantText {
			t.Errorf("%s: stored time = %q, want %q", row.Folder, text, row.WantText)
		}
		scan, err := store.GetScan(row.Folder)
		if err != nil || !scan.Time.Equal(row.WantTime) {
			t.Errorf("%s: scanned time = %v, %v, want %v", row.Folder, scan.Time, err, row.WantTime)
		}
	}

	var delivered []string
	for {
		scan, err := store.GetAvailableScan(t.Context(), "target", time.Date(2026, 10, 25, 1, 0, 0, 0, time.UTC))
		if errors.Is(err, errNoScans) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.completeDelivery("target", scan); err != nil {
			t.Fatal(err)
		}
		delivered = append(delivered, scan.Folder)
	}
	if want := []string{"mattn", "monotonic", "bst", "utc"}; !reflect.DeepEqual(delivered, want) {
		t.Errorf("delivery order = %v, want %v", delivered, want)
	}
}
