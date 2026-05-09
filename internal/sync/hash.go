package sync

import (
	"crypto/sha256"
	"encoding/hex"
)

// ContentHash returns the canonical "sha256:<hex>" identity of a byte
// blob. The emitter is what makes the input canonical (sorted keys,
// LF-only, single trailing newline, always-quoted user strings); this
// function is the thin wrapper that turns those bytes into the value
// stored in `sync_state.last_synced_hash` and embedded as
// `description_hash` / `body_hash` / `content_hash` in the YAML.
//
// We prefix with the algorithm name so a future rotation to (say)
// blake3 stays observable on disk without a schema change.
func ContentHash(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}
