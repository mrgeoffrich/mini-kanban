// Package minikanban exposes embedded asset bytes for the mk binary.
// It lives at the module root because //go:embed cannot traverse parent
// directories — by being a sibling of the .claude/ and examples/ trees
// it can reach the canonical SKILL.md and the sample-skills tree
// without copying.
package minikanban

import "embed"

//go:embed .claude/skills/mk/SKILL.md
var SkillMarkdown []byte

// SampleSkillsFS holds the bundled flow-level sample skills (file-issue,
// triage, stand-up, plan-feature) that `mk install-sample-skills` writes
// into a downstream repo's .claude/skills/. The FS is rooted at the
// module root, so each skill lives at examples/skills/<name>/SKILL.md.
//
//go:embed examples/skills
var SampleSkillsFS embed.FS
