package api_test

import (
	"strings"
	"testing"

	"github.com/mrgeoffrich/mini-kanban/internal/api"
	"github.com/mrgeoffrich/mini-kanban/internal/model"
)

func TestIssuesListEmpty(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	seedRepo(t, s)
	resp, body := apiGet(t, ts.URL+"/repos/MINI/issues")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d, body=%s", resp.StatusCode, body)
	}
	trimmed := strings.TrimSpace(string(body))
	if trimmed != "[]" {
		t.Fatalf("expected [], got %q", trimmed)
	}
}

func TestIssuesListRepoNotFound(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{})
	resp, _ := apiGet(t, ts.URL+"/repos/NONE/issues")
	if resp.StatusCode != 404 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestIssuesListPopulatedAndLeanByDefault(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	if _, err := s.CreateIssue(repo.ID, nil, "first", "long body", model.StateBacklog, nil); err != nil {
		t.Fatalf("create: %v", err)
	}
	resp, body := apiGet(t, ts.URL+"/repos/MINI/issues")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d, body=%s", resp.StatusCode, body)
	}
	if strings.Contains(string(body), "long body") {
		t.Fatalf("description leaked without with_description: %s", body)
	}
	resp2, body2 := apiGet(t, ts.URL+"/repos/MINI/issues?with_description=true")
	if resp2.StatusCode != 200 {
		t.Fatalf("status: %d", resp2.StatusCode)
	}
	if !strings.Contains(string(body2), "long body") {
		t.Fatalf("description missing with with_description=true: %s", body2)
	}
}

func TestIssuesListFilterByState(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	a, _ := s.CreateIssue(repo.ID, nil, "a", "", model.StateBacklog, nil)
	b, _ := s.CreateIssue(repo.ID, nil, "b", "", model.StateBacklog, nil)
	_ = s.SetIssueState(b.ID, model.StateDone)
	_ = a
	resp, body := apiGet(t, ts.URL+"/repos/MINI/issues?state=done")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d, body=%s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"key": "MINI-2"`) || strings.Contains(string(body), `"key": "MINI-1"`) {
		t.Fatalf("filter mismatch: %s", body)
	}
}

func TestIssuesListFilterByFeature(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	feat := seedFeature(t, s, repo, "auth", "Auth")
	if _, err := s.CreateIssue(repo.ID, &feat.ID, "with feat", "", model.StateBacklog, nil); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := s.CreateIssue(repo.ID, nil, "no feat", "", model.StateBacklog, nil); err != nil {
		t.Fatalf("create: %v", err)
	}
	resp, body := apiGet(t, ts.URL+"/repos/MINI/issues?feature=auth")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d, body=%s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"with feat"`) || strings.Contains(string(body), `"no feat"`) {
		t.Fatalf("feature filter mismatch: %s", body)
	}
}

func TestIssuesListFilterByTag(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	if _, err := s.CreateIssue(repo.ID, nil, "tagged", "", model.StateBacklog, []string{"ui"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := s.CreateIssue(repo.ID, nil, "untagged", "", model.StateBacklog, nil); err != nil {
		t.Fatalf("create: %v", err)
	}
	resp, body := apiGet(t, ts.URL+"/repos/MINI/issues?tag=ui")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d, body=%s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"tagged"`) || strings.Contains(string(body), `"untagged"`) {
		t.Fatalf("tag filter mismatch: %s", body)
	}
}

func TestIssueShowHappy(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "shown")
	resp, body := apiGet(t, ts.URL+"/repos/MINI/issues/"+iss.Key)
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d, body=%s", resp.StatusCode, body)
	}
	for _, want := range []string{`"issue"`, `"comments"`, `"relations"`, `"pull_requests"`, `"documents"`, `"shown"`} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("missing %s in: %s", want, body)
		}
	}
}

func TestIssueShowMalformedKey(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	seedRepo(t, s)
	resp, _ := apiGet(t, ts.URL+"/repos/MINI/issues/not-a-key")
	if resp.StatusCode != 400 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestIssueShowNotFound(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	seedRepo(t, s)
	resp, _ := apiGet(t, ts.URL+"/repos/MINI/issues/MINI-999")
	if resp.StatusCode != 404 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestIssueShowCrossRepoNotFound(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo1 := seedRepo(t, s)
	repo2 := seedRepo2(t, s)
	iss := seedIssue(t, s, repo2, "in other")
	_ = repo1
	resp, _ := apiGet(t, ts.URL+"/repos/MINI/issues/"+iss.Key)
	if resp.StatusCode != 404 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestIssuesReadDoesNotWriteHistory(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "x")
	apiGet(t, ts.URL+"/repos/MINI/issues")
	apiGet(t, ts.URL+"/repos/MINI/issues/"+iss.Key)
	assertHistoryOps(t, s, nil)
}

func TestIssueCreateHappy(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	seedRepo(t, s)
	resp, body := apiPost(t, ts.URL+"/repos/MINI/issues",
		`{"title":"a new issue","tags":["ui"]}`)
	if resp.StatusCode != 201 {
		t.Fatalf("status: %d, body=%s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"key": "MINI-1"`) {
		t.Fatalf("body: %s", body)
	}
	assertHistoryOps(t, s, []string{"issue.create"})
}

func TestIssueCreateDryRunQuery(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	seedRepo(t, s)
	resp, body := apiPost(t, ts.URL+"/repos/MINI/issues?dry_run=true",
		`{"title":"would be"}`)
	if resp.StatusCode != 201 {
		t.Fatalf("status: %d, body=%s", resp.StatusCode, body)
	}
	if got := resp.Header.Get("X-Dry-Run"); got != "applied" {
		t.Fatalf("X-Dry-Run header: %q", got)
	}
	if !strings.Contains(string(body), `"key": "MINI-1"`) {
		t.Fatalf("expected projected key MINI-1 in: %s", body)
	}
	assertHistoryOps(t, s, nil)
}

func TestIssueCreateDryRunHeader(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	seedRepo(t, s)
	resp, _ := apiReq(t, "POST", ts.URL+"/repos/MINI/issues",
		`{"title":"hdr"}`, map[string]string{"X-Dry-Run": "1"})
	if resp.StatusCode != 201 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Dry-Run"); got != "applied" {
		t.Fatalf("X-Dry-Run: %q", got)
	}
	assertHistoryOps(t, s, nil)
}

func TestIssueCreateTitleRequired(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	seedRepo(t, s)
	resp, _ := apiPost(t, ts.URL+"/repos/MINI/issues", `{}`)
	if resp.StatusCode != 400 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	assertHistoryOps(t, s, nil)
}

func TestIssueCreateUnknownField(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	seedRepo(t, s)
	resp, _ := apiPost(t, ts.URL+"/repos/MINI/issues", `{"title":"x","mystery":1}`)
	if resp.StatusCode != 400 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestIssueCreateUnknownFeature(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	seedRepo(t, s)
	resp, _ := apiPost(t, ts.URL+"/repos/MINI/issues",
		`{"title":"x","feature_slug":"nope"}`)
	if resp.StatusCode != 404 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	assertHistoryOps(t, s, nil)
}

func TestIssueCreateRepoNotFound(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{})
	resp, _ := apiPost(t, ts.URL+"/repos/NONE/issues", `{"title":"x"}`)
	if resp.StatusCode != 404 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestIssueStateHappy(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "x")
	resp, body := apiPut(t, ts.URL+"/repos/MINI/issues/"+iss.Key+"/state",
		`{"state":"todo"}`)
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d, body=%s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"state": "todo"`) {
		t.Fatalf("body: %s", body)
	}
	assertHistoryOps(t, s, []string{"issue.state"})
}

func TestIssueStateBadState(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "x")
	resp, _ := apiPut(t, ts.URL+"/repos/MINI/issues/"+iss.Key+"/state",
		`{"state":"bogus"}`)
	if resp.StatusCode != 400 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	assertHistoryOps(t, s, nil)
}

func TestIssueStateDryRun(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "x")
	resp, body := apiPut(t, ts.URL+"/repos/MINI/issues/"+iss.Key+"/state?dry_run=1",
		`{"state":"done"}`)
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	if resp.Header.Get("X-Dry-Run") != "applied" {
		t.Fatalf("X-Dry-Run header missing")
	}
	if !strings.Contains(string(body), `"state": "done"`) {
		t.Fatalf("body: %s", body)
	}
	assertHistoryOps(t, s, nil)
}

func TestIssueStateURLKeyWins(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	a := seedIssue(t, s, repo, "a")
	b := seedIssue(t, s, repo, "b")
	resp, _ := apiPut(t, ts.URL+"/repos/MINI/issues/"+a.Key+"/state",
		`{"key":"`+b.Key+`","state":"done"}`)
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	updatedA, _ := s.GetIssueByID(a.ID)
	updatedB, _ := s.GetIssueByID(b.ID)
	if updatedA.State != model.StateDone || updatedB.State == model.StateDone {
		t.Fatalf("URL did not win: a=%s b=%s", updatedA.State, updatedB.State)
	}
}

func TestIssueStateCrossRepo(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	repo2 := seedRepo2(t, s)
	iss := seedIssue(t, s, repo2, "x")
	_ = repo
	resp, _ := apiPut(t, ts.URL+"/repos/MINI/issues/"+iss.Key+"/state",
		`{"state":"done"}`)
	if resp.StatusCode != 404 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestIssueAssignHappy(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "x")
	resp, body := apiPut(t, ts.URL+"/repos/MINI/issues/"+iss.Key+"/assignee",
		`{"assignee":"alice"}`)
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d, body=%s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"assignee": "alice"`) {
		t.Fatalf("body: %s", body)
	}
	assertHistoryOps(t, s, []string{"issue.assign"})
}

func TestIssueAssignEmptyName(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "x")
	resp, _ := apiPut(t, ts.URL+"/repos/MINI/issues/"+iss.Key+"/assignee",
		`{"assignee":"  "}`)
	if resp.StatusCode != 400 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	assertHistoryOps(t, s, nil)
}

func TestIssueAssignDryRun(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "x")
	resp, body := apiPut(t, ts.URL+"/repos/MINI/issues/"+iss.Key+"/assignee?dry_run=true",
		`{"assignee":"bob"}`)
	if resp.StatusCode != 200 || resp.Header.Get("X-Dry-Run") != "applied" {
		t.Fatalf("status: %d, header: %q", resp.StatusCode, resp.Header.Get("X-Dry-Run"))
	}
	if !strings.Contains(string(body), `"assignee": "bob"`) {
		t.Fatalf("body: %s", body)
	}
	assertHistoryOps(t, s, nil)
}

func TestIssueUnassignHappy(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "x")
	if err := s.SetIssueAssignee(iss.ID, "alice"); err != nil {
		t.Fatal(err)
	}
	resp, body := apiDelete(t, ts.URL+"/repos/MINI/issues/"+iss.Key+"/assignee", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d, body=%s", resp.StatusCode, body)
	}
	if strings.Contains(string(body), `"assignee":`) {
		// assignee field has omitempty so an empty string drops it; just
		// confirm it isn't present.
		t.Fatalf("assignee should be cleared: %s", body)
	}
	assertHistoryOps(t, s, []string{"issue.assign"})
}

func TestIssueUnassignAlreadyEmptyNoHistory(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "x")
	resp, _ := apiDelete(t, ts.URL+"/repos/MINI/issues/"+iss.Key+"/assignee", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	assertHistoryOps(t, s, nil)
}

func TestIssueUnassignDryRun(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "x")
	_ = s.SetIssueAssignee(iss.ID, "alice")
	resp, _ := apiDelete(t, ts.URL+"/repos/MINI/issues/"+iss.Key+"/assignee?dry_run=1", nil)
	if resp.StatusCode != 200 || resp.Header.Get("X-Dry-Run") != "applied" {
		t.Fatalf("status: %d, header: %q", resp.StatusCode, resp.Header.Get("X-Dry-Run"))
	}
	assertHistoryOps(t, s, nil)
}
