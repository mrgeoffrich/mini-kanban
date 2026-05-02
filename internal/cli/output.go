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
	case *model.Issue:
		return printIssue(w, x)
	case []*model.Issue:
		for _, i := range x {
			feat := ""
			if i.FeatureSlug != "" {
				feat = " [" + i.FeatureSlug + "]"
			}
			fmt.Fprintf(w, "%-10s %-12s%s  %s\n", i.Key, i.State, feat, i.Title)
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
	case *model.Attachment:
		printAttachment(w, x)
	case []*model.Attachment:
		for _, a := range x {
			fmt.Fprintf(w, "%s\t%d bytes\n", a.Filename, a.SizeBytes)
		}
	case *store.IssueRelations:
		printRelations(w, x)
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
	Attachments  []*model.Attachment   `json:"attachments"`
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
	if len(v.Attachments) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Attachments:")
		for _, a := range v.Attachments {
			fmt.Fprintf(w, "  %s (%d bytes)\n", a.Filename, a.SizeBytes)
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

func printAttachment(w io.Writer, a *model.Attachment) {
	fmt.Fprintf(w, "%s\t%d bytes\n", a.Filename, a.SizeBytes)
	fmt.Fprintf(w, "Created: %s\n", localTime(a.CreatedAt))
	if a.Content != "" {
		fmt.Fprintln(w)
		fmt.Fprint(w, a.Content)
		if !strings.HasSuffix(a.Content, "\n") {
			fmt.Fprintln(w)
		}
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

type message struct {
	Text string `json:"message"`
}

func ok(format string, a ...any) error {
	return emit(message{Text: fmt.Sprintf(format, a...)})
}
