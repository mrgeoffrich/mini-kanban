// Package sync hosts the git-backed sync engine. Phase 1 only seeds the
// package with an identity helper so every record can carry a UUIDv7;
// the rest of the engine (engine.go, import.go, export.go, etc.) lands
// in later phases per docs/git-sync-implementation.md.
package sync

import "github.com/google/uuid"

// New returns a freshly-generated UUIDv7 string. UUIDv7 is time-ordered,
// which gives us roughly-chronological directory listings and stable
// ordering of comment filenames once those land. The store layer calls
// this on every Create* path so each record carries an immutable
// identity from birth; migrate() also calls it to backfill rows that
// pre-date the column.
//
// Panics on uuid.NewV7 failure. The underlying call only returns an
// error if the system's crypto/rand fails — at which point the process
// has bigger problems than a missing uuid, and treating it as a
// programming error keeps every call site one-line.
func New() string {
	id, err := uuid.NewV7()
	if err != nil {
		panic("sync: uuid.NewV7 failed: " + err.Error())
	}
	return id.String()
}
