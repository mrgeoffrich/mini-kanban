package api_test

import (
	"strings"
	"testing"

	"github.com/mrgeoffrich/mini-kanban/internal/api"
	"github.com/mrgeoffrich/mini-kanban/internal/model"
)

func TestFeaturesListEmpty(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	seedRepo(t, s)
	resp, body := apiGet(t, ts.URL+"/repos/MINI/features")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d, body=%s", resp.StatusCode, body)
	}
	if strings.TrimSpace(string(body)) != "[]" {
		t.Fatalf("expected [], got %q", body)
	}
}

func TestFeaturesListRepoNotFound(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{})
	resp, _ := apiGet(t, ts.URL+"/repos/NONE/features")
	if resp.StatusCode != 404 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestFeaturesListLeanByDefault(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	if _, err := s.CreateFeature(repo.ID, "auth", "Auth", "very long body"); err != nil {
		t.Fatalf("create: %v", err)
	}
	resp, body := apiGet(t, ts.URL+"/repos/MINI/features")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d, body=%s", resp.StatusCode, body)
	}
	if strings.Contains(string(body), "very long body") {
		t.Fatalf("description leaked without with_description: %s", body)
	}
	resp2, body2 := apiGet(t, ts.URL+"/repos/MINI/features?with_description=true")
	if resp2.StatusCode != 200 {
		t.Fatalf("status: %d", resp2.StatusCode)
	}
	if !strings.Contains(string(body2), "very long body") {
		t.Fatalf("description missing with with_description=true: %s", body2)
	}
}

func TestFeaturesListWithDescription1(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	if _, err := s.CreateFeature(repo.ID, "auth", "Auth", "the body"); err != nil {
		t.Fatalf("create: %v", err)
	}
	resp, body := apiGet(t, ts.URL+"/repos/MINI/features?with_description=1")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	if !strings.Contains(string(body), "the body") {
		t.Fatalf("description missing with with_description=1: %s", body)
	}
}

func TestFeatureShowHappy(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	feat := seedFeature(t, s, repo, "auth", "Auth")
	if _, err := s.CreateIssue(repo.ID, &feat.ID, "an issue", "", model.StateBacklog, nil); err != nil {
		t.Fatalf("create issue: %v", err)
	}
	resp, body := apiGet(t, ts.URL+"/repos/MINI/features/auth")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d, body=%s", resp.StatusCode, body)
	}
	for _, want := range []string{`"feature"`, `"issues"`, `"documents"`, `"an issue"`, `"slug": "auth"`} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("missing %s in: %s", want, body)
		}
	}
}

func TestFeatureShowEmptyArrays(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	seedFeature(t, s, repo, "empty", "Empty")
	resp, body := apiGet(t, ts.URL+"/repos/MINI/features/empty")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d, body=%s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"issues": []`) || !strings.Contains(string(body), `"documents": []`) {
		t.Fatalf("expected [] arrays, got: %s", body)
	}
}

func TestFeatureShowSlugNotFound(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	seedRepo(t, s)
	resp, _ := apiGet(t, ts.URL+"/repos/MINI/features/nope")
	if resp.StatusCode != 404 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestFeatureShowCrossRepoNotFound(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo1 := seedRepo(t, s)
	repo2 := seedRepo2(t, s)
	seedFeature(t, s, repo2, "auth", "Auth")
	_ = repo1
	resp, _ := apiGet(t, ts.URL+"/repos/MINI/features/auth")
	if resp.StatusCode != 404 {
		t.Fatalf("expected 404 cross-repo, got %d", resp.StatusCode)
	}
}

func TestFeaturesReadDoesNotWriteHistory(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	seedFeature(t, s, repo, "auth", "Auth")
	apiGet(t, ts.URL+"/repos/MINI/features")
	apiGet(t, ts.URL+"/repos/MINI/features/auth")
	assertHistoryOps(t, s, nil)
}
