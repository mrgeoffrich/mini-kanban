// Package minikanban exposes embedded asset bytes for the mk binary.
// It lives at the module root because //go:embed cannot traverse parent
// directories — by being a sibling of the .claude/ tree it can reach
// the canonical SKILL.md without copying.
package minikanban

import _ "embed"

//go:embed .claude/skills/mk/SKILL.md
var SkillMarkdown []byte
