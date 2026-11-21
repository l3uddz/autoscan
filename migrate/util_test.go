package migrate

import (
	"strings"
	"testing"
	"testing/fstest"
)

func TestMigrationVersion(t *testing.T) {
	type Test struct {
		Name    string
		File    string
		Want    int
		WantErr bool
	}

	var testCases = []Test{
		{Name: "Init migration", File: "1_init.sql", Want: 1},
		{Name: "Scan revision migration", File: "2_scan_revision.sql", Want: 2},
		{Name: "Target delivery migration", File: "3_target_delivery.sql", Want: 3},
		{Name: "Test migration", File: "1_orphan_row.sql", Want: 1},
		{Name: "Multi-digit version", File: "12_add_index.sql", Want: 12},
		{Name: "Version without a name", File: "4.sql", Want: 4},
		{Name: "Not a migration", File: "README.md", WantErr: true},
		{Name: "Version after a prefix", File: "backup_1_init.sql", WantErr: true},
		{Name: "Text after the extension", File: "1_init.sql.bak", WantErr: true},
	}

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			got, err := migrationVersion(tc.File)
			if tc.WantErr {
				if err == nil {
					t.Fatalf("migrationVersion(%q) = %d, want an error", tc.File, got)
				}
				return
			}

			if err != nil || got != tc.Want {
				t.Fatalf("migrationVersion(%q) = %d, %v, want %d, nil", tc.File, got, err, tc.Want)
			}
		})
	}
}

func TestParseRejectsDuplicateVersions(t *testing.T) {
	fsys := fstest.MapFS{
		"migrations/1_first.sql":  {Data: []byte("SELECT 1;")},
		"migrations/1_second.sql": {Data: []byte("SELECT 2;")},
	}

	m := &Migrator{dir: "migrations"}
	_, err := m.parse(fsys)
	if err == nil || !strings.Contains(err.Error(), "duplicate migration version 1") {
		t.Fatalf("parse returned %v, want a duplicate migration version error", err)
	}
}
