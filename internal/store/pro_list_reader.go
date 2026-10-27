package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
)

// A list request uses its own read-only WAL snapshot. It must not queue behind
// the application's write mutex or keep that mutex while calculating finances.
// This store is request-local: no initialization, writes, or background workers.
func (s *Store) ProListReader(ctx context.Context) (*Store, error) {
	path := strings.NewReplacer("%", "%25", "#", "%23", "?", "%3F").Replace(filepath.ToSlash(s.path))
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(1000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	// All reads on this dedicated connection share one view, including counts,
	// account rows, settings and financial projections. Closing rolls it back.
	if _, err = db.ExecContext(ctx, "BEGIN"); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{path: s.path, key: s.key, db: db}, nil
}
