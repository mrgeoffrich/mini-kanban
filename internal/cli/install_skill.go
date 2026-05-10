package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	root "github.com/mrgeoffrich/mini-kanban"
	"github.com/mrgeoffrich/mini-kanban/internal/git"
)

func newInstallSkillCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install-skill",
		Short: "Install the mk Claude Code skill into the current repo",
		Long: `Drop the bundled SKILL.md into <repo-root>/.claude/skills/mk/, creating
the directory if needed. Overwrites any existing copy with the version
embedded in this build of mk so re-running picks up doc updates.

See also: 'mk install-sample-skills' for the flow-level workflow skills
(file-issue, triage, stand-up, plan-feature) that build on top of this
canonical reference.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if inRemoteMode() {
				return fmt.Errorf("mk install-skill: not supported in remote mode (writes the skill file to the local repo); run this verb against the local DB instead")
			}
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			info, err := git.Detect(cwd)
			if err != nil {
				return err
			}
			dir := filepath.Join(info.Root, ".claude", "skills", "mk")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return fmt.Errorf("create skill dir: %w", err)
			}
			path := filepath.Join(dir, "SKILL.md")
			if err := os.WriteFile(path, root.SkillMarkdown, 0o644); err != nil {
				return fmt.Errorf("write skill: %w", err)
			}
			return ok("installed mk skill (%d bytes) at %s", len(root.SkillMarkdown), path)
		},
	}
}
