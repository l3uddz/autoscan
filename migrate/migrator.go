// Package migrate applies the embedded SQL migrations of a component to the
// database in order, and records each migration that it applied.
package migrate

import (
	"database/sql"
	"errors"
	"fmt"
	"io/fs"

	"modernc.org/sqlite"
)

type Migrator struct {
	db  *sql.DB
	dir string
}

/* Credits to https://github.com/Boostport/migration */

func New(db *sql.DB, dir string) (*Migrator, error) {
	m := &Migrator{
		db:  db,
		dir: dir,
	}

	// validate supported driver
	if _, ok := db.Driver().(*sqlite.Driver); !ok {
		return nil, errors.New("database instance is not using the sqlite driver")
	}

	// verify schema
	if err := m.verify(); err != nil {
		return nil, fmt.Errorf("verify: %w", err)
	}

	return m, nil
}

// Migrate runs the migrations in the directory of m in fsys that component
// has not run yet, in the order of their versions.
func (m *Migrator) Migrate(fsys fs.FS, component string) error {
	// parse migrations
	migrations, err := m.parse(fsys)
	if err != nil {
		return fmt.Errorf("parse: %w", err)
	}

	if len(migrations) == 0 {
		return nil
	}

	// get current migration versions
	versions, err := m.versions(component)
	if err != nil {
		return fmt.Errorf("versions: %v: %w", component, err)
	}

	// migrate
	for _, mg := range migrations {
		// already have this version?
		if _, exists := versions[mg.Version]; exists {
			continue
		}

		// migrate
		if err := m.exec(component, mg); err != nil {
			return fmt.Errorf("migrate: %v: %w", mg.Filename, err)
		}
	}

	return nil
}
