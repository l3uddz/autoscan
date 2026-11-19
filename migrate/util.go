package migrate

import (
	"embed"
	"errors"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
)

type migration struct {
	Version  int
	Filename string
	Schema   string
}

// migrationName matches a migration file name and captures its version.
var migrationName = regexp.MustCompile(`(?P<Version>\d+)\w?(?P<Name>.+)?\.sql`)

// migrationVersion returns the version in a migration file name.
func migrationVersion(name string) (int, error) {
	match := migrationName.FindStringSubmatch(name)
	if match == nil {
		return 0, errors.New("no match found for given string")
	}

	return strconv.Atoi(match[migrationName.SubexpIndex("Version")])
}

func (m *Migrator) verify() error {
	if _, err := m.db.Exec(sqlSchema); err != nil {
		return fmt.Errorf("schema: %w", err)
	}
	return nil
}

func (m *Migrator) versions(component string) (map[int]bool, error) {
	rows, err := m.db.Query(sqlVersions, component)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	versions := make(map[int]bool, 0)
	for rows.Next() {
		var version int
		if err := rows.Scan(&version); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}

		versions[version] = true
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows: %w", err)
	}

	return versions, nil
}

func (m *Migrator) exec(component string, migration *migration) error {
	tx, err := m.db.Begin()
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback()

	// exec migration
	if _, err := tx.Exec(migration.Schema); err != nil {
		return fmt.Errorf("exec: %w", err)
	}

	// insert migration version
	if _, err := tx.Exec(sqlInsertVersion, component, migration.Version); err != nil {
		return fmt.Errorf("schema_migration: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	return nil
}

func (m *Migrator) parse(fs *embed.FS) ([]*migration, error) {
	// parse migrations from filesystem
	files, err := fs.ReadDir(m.dir)
	if err != nil {
		return nil, fmt.Errorf("read migrations: %w", err)
	}

	// parse migrations
	migrations := make([]*migration, 0)
	for _, f := range files {
		// skip dirs
		if f.IsDir() {
			continue
		}

		// parse migration
		version, err := migrationVersion(f.Name())
		if err != nil {
			return nil, fmt.Errorf("parse migration: %w", err)
		}

		b, err := fs.ReadFile(path.Join(m.dir, f.Name()))
		if err != nil {
			return nil, fmt.Errorf("read migration: %w", err)
		}

		// set migration
		migrations = append(migrations, &migration{
			Version:  version,
			Filename: f.Name(),
			Schema:   string(b),
		})
	}

	// sort migrations
	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].Version < migrations[j].Version
	})

	return migrations, nil
}

const (
	sqlSchema        = `CREATE TABLE IF NOT EXISTS schema_migration (component VARCHAR(255) NOT NULL, version INTEGER NOT NULL, PRIMARY KEY (component, version))`
	sqlVersions      = `SELECT version FROM schema_migration WHERE component = ?`
	sqlInsertVersion = `INSERT INTO schema_migration (component, version) VALUES (?, ?)`
)
