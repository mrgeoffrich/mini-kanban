package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"mini-kanban/internal/model"
	"mini-kanban/internal/store"
)

// localTime renders a UTC timestamp in the user's local timezone for text
// output. JSON marshalling uses Go's default RFC 3339 / UTC, untouched.
func localTime(t time.Time) string {
	return t.Local().Format("2006-01-02 15:04 MST")
}

func emit(v any) error {
	if opts.output == outputJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(v)
	}
	return renderText(os.Stdout, v)
}

func renderText(w io.Writer, v any) error {
	switch x := v.(type) {
	case *model.Repo:
		return printRepo(w, x)
	case []*model.Repo:
		for _, r := range x {
			fmt.Fprintf(w, "%s  %s\t%s\n", r.Prefix, r.Name, r.Path)
		}
	case *model.Feature:
		fmt.Fprintf(w, "%s\t%s\n", x.Slug, x.Title)
		fmt.Fprintf(w, "Created:  %s\n", localTime(x.CreatedAt))
		fmt.Fprintf(w, "Updated:  %s\n", localTime(x.UpdatedAt))
		if x.Description != "" {
			fmt.Fprintln(w)
			fmt.Fprintln(w, x.Description)
		}
	case []*model.Feature:
		for _, f := range x {
			fmt.Fprintf(w, "%s\t%s\n", f.Slug, f.Title)
		}
	case *issueView:
		return printIssueView(w, x)
	case *featureView:
		return printFeatureView(w, x)
	case *model.Issue:
		return printIssue(w, x)
	case []*model.Issue:
		for _, i := range x {
			feat := ""
			if i.FeatureSlug != "" {
				feat = " [" + i.FeatureSlug + "]"
			}
			tagStr := ""
			if len(i.Tags) > 0 {
				tagStr = "  #" + strings.Join(i.Tags, " #")
			}
			fmt.Fprintf(w, "%-10s %-12s%s  %s%s\n", i.Key, i.State, feat, i.Title, tagStr)
		}
	case *model.Comment:
		fmt.Fprintf(w, "%s — %s\n%s\n", x.Author, localTime(x.CreatedAt), x.Body)
	case []*model.Comment:
		for _, c := range x {
			fmt.Fprintf(w, "%s — %s\n%s\n\n", c.Author, localTime(c.CreatedAt), c.Body)
		}
	case *model.PullRequest:
		fmt.Fprintln(w, x.URL)
	case []*model.PullRequest:
		for _, pr := range x {
			fmt.Fprintln(w, pr.URL)
		}
	case *model.Document:
		printDocument(w, x)
	case []*model.Document:
		for _, d := range x {
			fmt.Fprintf(w, "%-30s %-22s %d bytes\n", d.Filename, d.Type, d.SizeBytes)
		}
	case *model.DocumentLink:
		printDocLinkLine(w, x)
	case []*model.DocumentLink:
		for _, l := range x {
			printDocLinkLine(w, l)
		}
	case *docView:
		return printDocView(w, x)
	case *store.IssueRelations:
		printRelations(w, x)
	case []*model.HistoryEntry:
		for _, e := range x {
			printHistoryLine(w, e)
		}
	case message:
		fmt.Fprintln(w, x.Text)
	default:
		fmt.Fprintf(w, "%v\n", v)
	}
	return nil
}

func printRepo(w io.Writer, r *model.Repo) error {
	fmt.Fprintf(w, "Prefix:    %s\n", r.Prefix)
	fmt.Fprintf(w, "Name:      %s\n", r.Name)
	fmt.Fprintf(w, "Path:      %s\n", r.Path)
	if r.RemoteURL != "" {
		fmt.Fprintf(w, "Remote:    %s\n", r.RemoteURL)
	}
	fmt.Fprintf(w, "NextIssue: %s-%d\n", r.Prefix, r.NextIssueNumber)
	fmt.Fprintf(w, "Created:   %s\n", localTime(r.CreatedAt))
	return nil
}

func printIssue(w io.Writer, i *model.Issue) error {
	fmt.Fprintf(w, "%s  %s\n", i.Key, i.Title)
	fmt.Fprintf(w, "State:    %s\n", i.State)
	if i.FeatureSlug != "" {
		fmt.Fprintf(w, "Feature:  %s\n", i.FeatureSlug)
	}
	if len(i.Tags) > 0 {
		fmt.Fprintf(w, "Tags:     %s\n", strings.Join(i.Tags, ", "))
	}
	fmt.Fprintf(w, "Created:  %s\n", localTime(i.CreatedAt))
	fmt.Fprintf(w, "Updated:  %s\n", localTime(i.UpdatedAt))
	if i.Description != "" {
		fmt.Fprintf(w, "\n%s\n", i.Description)
	}
	return nil
}

type issueView struct {
	Issue        *model.Issue          `json:"issue"`
	Comments     []*model.Comment      `json:"comments"`
	Relations    *store.IssueRelations `json:"relations"`
	PullRequests []*model.PullRequest  `json:"pull_requests"`
	Documents    []*model.DocumentLink `json:"documents"`
}

type featureView struct {
	Feature   *model.Feature        `json:"feature"`
	Issues    []*model.Issue        `json:"issues"`
	Documents []*model.DocumentLink `json:"documents"`
}

func printIssueView(w io.Writer, v *issueView) error {
	if err := printIssue(w, v.Issue); err != nil {
		return err
	}
	if len(v.PullRequests) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Pull requests:")
		for _, pr := range v.PullRequests {
			fmt.Fprintf(w, "  %s\n", pr.URL)
		}
	}
	if v.Relations != nil && (len(v.Relations.Outgoing) > 0 || len(v.Relations.Incoming) > 0) {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Relations:")
		printRelations(w, v.Relations)
	}
	if len(v.Documents) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Linked documents:")
		for _, l := range v.Documents {
			if l.Description != "" {
				fmt.Fprintf(w, "  %s — %s\n", l.DocumentFilename, l.Description)
			} else {
				fmt.Fprintf(w, "  %s\n", l.DocumentFilename)
			}
		}
	}
	if len(v.Comments) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Comments:")
		fmt.Fprintln(w, strings.Repeat("-", 40))
		for _, c := range v.Comments {
			fmt.Fprintf(w, "%s — %s\n%s\n\n", c.Author, localTime(c.CreatedAt), c.Body)
		}
	}
	return nil
}

func printFeatureView(w io.Writer, v *featureView) error {
	f := v.Feature
	fmt.Fprintf(w, "%s\t%s\n", f.Slug, f.Title)
	fmt.Fprintf(w, "Created:  %s\n", localTime(f.CreatedAt))
	fmt.Fprintf(w, "Updated:  %s\n", localTime(f.UpdatedAt))
	if f.Description != "" {
		fmt.Fprintln(w)
		fmt.Fprintln(w, f.Description)
	}
	if len(v.Issues) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Issues:")
		for _, i := range v.Issues {
			fmt.Fprintf(w, "  %-10s %-12s %s\n", i.Key, i.State, i.Title)
		}
	}
	if len(v.Documents) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Linked documents:")
		for _, l := range v.Documents {
			if l.Description != "" {
				fmt.Fprintf(w, "  %s — %s\n", l.DocumentFilename, l.Description)
			} else {
				fmt.Fprintf(w, "  %s\n", l.DocumentFilename)
			}
		}
	}
	return nil
}

type docView struct {
	Document *model.Document        `json:"document"`
	Links    []*model.DocumentLink  `json:"links"`
}

func printDocument(w io.Writer, d *model.Document) {
	fmt.Fprintf(w, "%s  type=%s  %d bytes\n", d.Filename, d.Type, d.SizeBytes)
	fmt.Fprintf(w, "Created: %s\n", localTime(d.CreatedAt))
	fmt.Fprintf(w, "Updated: %s\n", localTime(d.UpdatedAt))
}

func printDocView(w io.Writer, v *docView) error {
	printDocument(w, v.Document)
	if v.Document.Content != "" {
		fmt.Fprintln(w)
		fmt.Fprint(w, v.Document.Content)
		if !strings.HasSuffix(v.Document.Content, "\n") {
			fmt.Fprintln(w)
		}
	}
	if len(v.Links) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Linked to:")
		for _, l := range v.Links {
			printDocLinkLine(w, l)
		}
	}
	return nil
}

func printDocLinkLine(w io.Writer, l *model.DocumentLink) {
	target := l.Target()
	if l.Description != "" {
		fmt.Fprintf(w, "  %s — %s\n", target, l.Description)
	} else {
		fmt.Fprintf(w, "  %s\n", target)
	}
}

func printRelations(w io.Writer, r *store.IssueRelations) {
	for _, rel := range r.Outgoing {
		fmt.Fprintf(w, "  %s %s\n", rel.Type, rel.ToIssue)
	}
	for _, rel := range r.Incoming {
		fmt.Fprintf(w, "  %s by %s\n", rel.Type, rel.FromIssue)
	}
}

func printHistoryLine(w io.Writer, e *model.HistoryEntry) {
	target := e.TargetLabel
	if target == "" {
		target = "-"
	}
	if e.RepoPrefix != "" && e.Kind != "repo" {
		target = e.RepoPrefix + "/" + target
	}
	line := fmt.Sprintf("%s  %-12s %-22s %s",
		localTime(e.CreatedAt), e.Actor, e.Op, target)
	if e.Details != "" {
		line += "  " + e.Details
	}
	fmt.Fprintln(w, line)
}

type message struct {
	Text string `json:"message"`
}

func ok(format string, a ...any) error {
	return emit(message{Text: fmt.Sprintf(format, a...)})
}
