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
