package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"

	"github.com/invopop/jsonschema"
	"github.com/spf13/cobra"

	"github.com/mrgeoffrich/mini-kanban/internal/cli/inputs"
)

// schemaEntry registers one mutating command's JSON-input schema. The
// dotted name (e.g. "issue.add") matches the cobra command path with
// spaces replaced by dots — the same convention used for history op
// names like "issue.create".
type schemaEntry struct {
	Name        string
	Short       string
	InputType   reflect.Type
	Example     any
}

func t[T any]() reflect.Type { return reflect.TypeOf((*T)(nil)).Elem() }

// schemaRegistry is the single source of truth for what `mk schema` knows
// about. Adding a mutating command means adding one row here; the runtime
// schema and the cobra command stay aligned because both consume the same
// inputs.*Input struct.
var schemaRegistry = []schemaEntry{
	{"issue.add", "Create an issue in the current repo.", t[inputs.IssueAddInput](), inputs.ExampleIssueAdd},
	{"issue.edit", "Update an issue's title, description, or feature.", t[inputs.IssueEditInput](), inputs.ExampleIssueEdit},
	{"issue.state", "Set an issue's state.", t[inputs.IssueStateInput](), inputs.ExampleIssueState},
	{"issue.assign", "Assign an issue to a person or agent.", t[inputs.IssueAssignInput](), inputs.ExampleIssueAssign},
	{"issue.unassign", "Clear an issue's assignee.", t[inputs.IssueUnassignInput](), inputs.ExampleIssueUnassign},
	{"issue.next", "Atomically claim the next ready issue in a feature.", t[inputs.IssueNextInput](), inputs.ExampleIssueNext},
	{"issue.rm", "Delete an issue (and its comments).", t[inputs.IssueRmInput](), inputs.ExampleIssueRm},

	{"feature.add", "Create a feature in the current repo.", t[inputs.FeatureAddInput](), inputs.ExampleFeatureAdd},
	{"feature.edit", "Update a feature's title or description.", t[inputs.FeatureEditInput](), inputs.ExampleFeatureEdit},
	{"feature.rm", "Delete a feature (issues are kept, unlinked).", t[inputs.FeatureRmInput](), inputs.ExampleFeatureRm},

	{"comment.add", "Add a comment to an issue.", t[inputs.CommentAddInput](), inputs.ExampleCommentAdd},

	{"link", "Create a relation (blocks, relates-to, duplicate-of) between two issues.", t[inputs.LinkInput](), inputs.ExampleLink},
	{"unlink", "Remove all relations between two issues.", t[inputs.UnlinkInput](), inputs.ExampleUnlink},

	{"pr.attach", "Attach a pull-request URL to an issue.", t[inputs.PRAttachInput](), inputs.ExamplePRAttach},
	{"pr.detach", "Detach a pull-request URL from an issue.", t[inputs.PRDetachInput](), inputs.ExamplePRDetach},

	{"tag.add", "Add tags to an issue (idempotent).", t[inputs.TagAddInput](), inputs.ExampleTagAdd},
	{"tag.rm", "Remove tags from an issue.", t[inputs.TagRmInput](), inputs.ExampleTagRm},

	{"doc.add", "Create a document in the current repo.", t[inputs.DocAddInput](), inputs.ExampleDocAdd},
	{"doc.upsert", "Create or update a document (same shape as doc.add).", t[inputs.DocAddInput](), inputs.ExampleDocAdd},
	{"doc.edit", "Edit a document's type and/or content.", t[inputs.DocEditInput](), inputs.ExampleDocEdit},
	{"doc.rename", "Rename a document, preserving its links.", t[inputs.DocRenameInput](), inputs.ExampleDocRename},
	{"doc.rm", "Delete a document and its links.", t[inputs.DocRmInput](), inputs.ExampleDocRm},
	{"doc.link", "Link a document to an issue or feature.", t[inputs.DocLinkInput](), inputs.ExampleDocLink},
	{"doc.unlink", "Remove a document's link to an issue or feature.", t[inputs.DocUnlinkInput](), inputs.ExampleDocUnlink},
	{"doc.export", "Write a document's content to disk.", t[inputs.DocExportInput](), inputs.ExampleDocExport},
}

func newSchemaCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "schema",
		Short: "Publish JSON Schemas for every mutating command's --json payload",
		Long: `Runtime schema introspection for agents driving mk via --json.

Each schema describes the JSON shape one mutating command accepts on its
--json flag. Schema names are dotted forms of the cobra command path
(e.g. "mk issue add" → "issue.add"). The schema set here mirrors the
strict decoder, so any payload that matches the schema will decode
without "unknown field" errors.`,
	}
	cmd.AddCommand(schemaListCmd(), schemaShowCmd(), schemaAllCmd())
	return cmd
}

func schemaListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List every command name with a published JSON-input schema",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if opts.output == outputJSON {
				type row struct {
					Name        string `json:"name"`
					Description string `json:"description"`
				}
				out := make([]row, 0, len(schemaRegistry))
				for _, e := range schemaRegistry {
					out = append(out, row{e.Name, e.Short})
				}
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(out)
			}
			width := 0
			for _, e := range schemaRegistry {
				if len(e.Name) > width {
					width = len(e.Name)
				}
			}
			for _, e := range schemaRegistry {
				fmt.Fprintf(os.Stdout, "%-*s  %s\n", width, e.Name, e.Short)
			}
			return nil
		},
	}
}

func schemaShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <name>",
		Short: "Print the JSON Schema for a single command (e.g. \"issue.add\")",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			entry, ok := findSchema(args[0])
			if !ok {
				return fmt.Errorf("unknown schema %q; run `mk schema list` to see all names", args[0])
			}
			schema := buildSchema(entry)
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(schema)
		},
	}
}

func schemaAllCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "all",
		Short: "Print every published schema as one JSON object keyed by command name",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := make(map[string]*jsonschema.Schema, len(schemaRegistry))
			names := make([]string, 0, len(schemaRegistry))
			for _, e := range schemaRegistry {
				out[e.Name] = buildSchema(e)
				names = append(names, e.Name)
			}
			sort.Strings(names)
			// Use OrderedMap-style emission via a manual buffer so the keys
			// land in registry order rather than Go's randomised map order.
			fmt.Fprint(os.Stdout, "{\n")
			for i, name := range names {
				body, err := json.MarshalIndent(out[name], "  ", "  ")
				if err != nil {
					return err
				}
				sep := ","
				if i == len(names)-1 {
					sep = ""
				}
				fmt.Fprintf(os.Stdout, "  %q: %s%s\n", name, body, sep)
			}
			fmt.Fprint(os.Stdout, "}\n")
			return nil
		},
	}
}

func findSchema(name string) (schemaEntry, bool) {
	name = strings.TrimSpace(name)
	for _, e := range schemaRegistry {
		if e.Name == name {
			return e, true
		}
	}
	return schemaEntry{}, false
}

// buildSchema reflects a registry entry's input type into a JSON Schema and
// attaches the entry's metadata (title, description, examples).
func buildSchema(e schemaEntry) *jsonschema.Schema {
	r := &jsonschema.Reflector{
		Anonymous:      true,
		ExpandedStruct: true,
		DoNotReference: true,
	}
	s := r.ReflectFromType(e.InputType)
	s.Version = "https://json-schema.org/draft/2020-12/schema"
	s.ID = jsonschema.ID(fmt.Sprintf("mk://schema/%s", e.Name))
	s.Title = e.InputType.Name()
	s.Description = e.Short
	if e.Example != nil {
		s.Examples = []any{e.Example}
	}
	return s
}
