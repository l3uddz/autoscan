package migrate

import "testing"

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
