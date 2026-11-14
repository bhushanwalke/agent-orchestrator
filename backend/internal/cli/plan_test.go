package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type planRequest struct {
	Method string
	Path   string
	Query  string
	Body   map[string]any
}

const planDetailJSON = `{"plan":{"id":"plan_1","projectId":"demo","title":"Demo","state":"active","phase":"implementing","displayStatus":"Implementing 1/2","kanbanColumn":"building","maxParallel":3,"orchestratorSessionId":"orch-1"},
"milestones":[
 {"key":"m1","kind":"implement","status":"done","dependsOn":[],"session":{"id":"w-1"}},
 {"key":"m2","kind":"implement","status":"pending","dependsOn":["m1"]},
 {"key":"qa","kind":"qa","status":"pending","dependsOn":[]}],
"events":[{"id":3,"milestoneKey":"m1","kind":"milestone_done","note":"built it","createdAt":"2026-11-14T09:00:00Z"}]}`

// newPlanServer records every plan API request and answers with canned
// bodies. A response keyed "METHOD path" overrides the default.
func newPlanServer(t *testing.T, overrides map[string]struct {
	status int
	body   string
}) (*[]planRequest, *httptest.Server) {
	t.Helper()
	var mu sync.Mutex
	var reqs []planRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/internal/telemetry/cli-invoked":
			w.WriteHeader(http.StatusAccepted)
			return
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/projects/demo":
			_, _ = io.WriteString(w, `{"project":{"id":"demo","kind":"single_repo"}}`)
			return
		}
		req := planRequest{Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery}
		if raw, _ := io.ReadAll(r.Body); len(raw) > 0 {
			if err := json.Unmarshal(raw, &req.Body); err != nil {
				t.Errorf("decode body %s: %v", raw, err)
			}
		}
		mu.Lock()
		reqs = append(reqs, req)
		mu.Unlock()
		if o, ok := overrides[r.Method+" "+r.URL.Path]; ok {
			w.WriteHeader(o.status)
			_, _ = io.WriteString(w, o.body)
			return
		}
		switch {
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		case strings.HasSuffix(r.URL.Path, "/milestones") && r.Method == http.MethodPost:
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"milestone":{"key":"m1","kind":"implement","status":"pending","dependsOn":[]}}`)
		case strings.Contains(r.URL.Path, "/milestones/") && r.Method == http.MethodPatch:
			_, _ = io.WriteString(w, `{"milestone":{"key":"m1","kind":"implement","status":"pending","dependsOn":[]}}`)
		case strings.HasSuffix(r.URL.Path, "/plans") && r.Method == http.MethodPost:
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"plan":{"id":"plan_1","projectId":"demo","title":"Demo","state":"planning","phase":"planning","orchestratorSessionId":"orch-1"}}`)
		case strings.HasSuffix(r.URL.Path, "/plans"):
			_, _ = io.WriteString(w, `{"plans":[{"id":"plan_1","title":"Demo","state":"planning","phase":"planning","displayStatus":"Planning","milestoneCounts":{"total":2,"done":0}}]}`)
		case strings.HasSuffix(r.URL.Path, "/events"):
			_, _ = io.WriteString(w, `{"events":[{"id":4,"milestoneKey":"","kind":"plan_created","note":"","createdAt":"2026-11-14T09:00:00Z"}],"nextAfter":4}`)
		case strings.HasSuffix(r.URL.Path, "/cancel") || r.Method == http.MethodPatch:
			_, _ = io.WriteString(w, `{"plan":{"id":"plan_1","state":"cancelled","phase":"cancelled"}}`)
		default:
			_, _ = io.WriteString(w, planDetailJSON)
		}
	}))
	t.Cleanup(server.Close)
	return &reqs, server
}

func runPlanCLI(t *testing.T, args ...string) ([]planRequest, string, error) {
	t.Helper()
	cfg := setConfigEnv(t)
	reqs, server := newPlanServer(t, nil)
	writeRunFileFor(t, cfg, server)
	out, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, args...)
	return *reqs, out, err
}

func TestPlanCommandsSendExpectedRequests(t *testing.T) {
	dir := t.TempDir()
	briefFile := filepath.Join(dir, "brief.md")
	descFile := filepath.Join(dir, "desc.md")
	specFile := filepath.Join(dir, "plan.md")
	for path, body := range map[string]string{briefFile: "Brief from file", descFile: "Desc from file", specFile: "# Plan"} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		name   string
		args   []string
		method string
		path   string
		query  string
		body   map[string]any
		out    string
	}{
		{"create", []string{"plan", "create", "--project", "demo", "--title", "Demo", "--brief", "Add X", "--ticket", "github:o/r#1", "--max-parallel", "2"},
			"POST", "/api/v1/projects/demo/plans", "", map[string]any{"title": "Demo", "brief": "Add X", "ticketRef": "github:o/r#1", "maxParallel": float64(2)}, "plan_1"},
		{"create brief file", []string{"plan", "create", "--project", "demo", "--title", "Demo", "--brief-file", briefFile},
			"POST", "/api/v1/projects/demo/plans", "", map[string]any{"title": "Demo", "brief": "Brief from file"}, "plan_1"},
		{"ls", []string{"plan", "ls", "--project", "demo"}, "GET", "/api/v1/projects/demo/plans", "", nil, "Planning"},
		{"get", []string{"plan", "get", "plan_1"}, "GET", "/api/v1/plans/plan_1", "", nil, "milestone_done"},
		{"set-spec", []string{"plan", "set-spec", "plan_1", "--file", specFile}, "PATCH", "/api/v1/plans/plan_1", "", map[string]any{"specMarkdown": "# Plan"}, ""},
		{"milestone add", []string{"plan", "milestone", "add", "plan_1", "--key", "m2", "--title", "B", "--description-file", descFile, "--depends-on", "m1,m0", "--sparse", "src/api", "--sparse", "src/shared"},
			"POST", "/api/v1/plans/plan_1/milestones", "", map[string]any{"key": "m2", "kind": "implement", "title": "B", "description": "Desc from file",
				"dependsOn": []any{"m1", "m0"}, "sparsePaths": []any{"src/api", "src/shared"}}, "m1"},
		{"milestone update only changed", []string{"plan", "milestone", "update", "plan_1", "m1", "--depends-on", "m3"},
			"PATCH", "/api/v1/plans/plan_1/milestones/m1", "", map[string]any{"dependsOn": []any{"m3"}}, ""},
		{"milestone update clears deps", []string{"plan", "milestone", "update", "plan_1", "m1", "--depends-on", ""},
			"PATCH", "/api/v1/plans/plan_1/milestones/m1", "", map[string]any{"dependsOn": []any{}}, ""},
		{"milestone rm", []string{"plan", "milestone", "rm", "plan_1", "m1"}, "DELETE", "/api/v1/plans/plan_1/milestones/m1", "", nil, "removed"},
		{"milestone retry", []string{"plan", "milestone", "retry", "plan_1", "m1"}, "POST", "/api/v1/plans/plan_1/milestones/m1/retry", "", nil, ""},
		{"activate", []string{"plan", "activate", "plan_1"}, "POST", "/api/v1/plans/plan_1/activate", "", nil, "implementing"},
		{"cancel yes", []string{"plan", "cancel", "plan_1", "--yes"}, "POST", "/api/v1/plans/plan_1/cancel", "", nil, "cancelled"},
		{"events", []string{"plan", "events", "plan_1", "--after", "3"}, "GET", "/api/v1/plans/plan_1/events", "after=3", nil, "plan_created"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("AO_PLAN_ID", "")
			t.Setenv("AO_SESSION_ID", "")
			reqs, out, err := runPlanCLI(t, tc.args...)
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			if len(reqs) != 1 {
				t.Fatalf("requests = %+v", reqs)
			}
			got := reqs[0]
			if got.Method != tc.method || got.Path != tc.path || got.Query != tc.query {
				t.Fatalf("request = %s %s?%s", got.Method, got.Path, got.Query)
			}
			if tc.body != nil {
				gotJSON, _ := json.Marshal(got.Body)
				wantJSON, _ := json.Marshal(tc.body)
				if string(gotJSON) != string(wantJSON) {
					t.Fatalf("body = %s want %s", gotJSON, wantJSON)
				}
			}
			if !strings.Contains(out, tc.out) {
				t.Fatalf("output %q missing %q", out, tc.out)
			}
		})
	}
}

func TestPlanGetPrintsMilestoneTable(t *testing.T) {
	t.Setenv("AO_PLAN_ID", "")
	_, out, err := runPlanCLI(t, "plan", "get", "plan_1")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Implementing 1/2", "KEY", "KIND", "STATUS", "DEPS", "SESSION", "m2", "m1", "w-1"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
	header := ""
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "KEY") {
			header = strings.Join(strings.Fields(line), " ")
		}
	}
	if header != "KEY KIND STATUS DEPS SESSION" {
		t.Fatalf("header = %q", header)
	}
}

func TestPlanIDFallsBackToAOPlanID(t *testing.T) {
	t.Setenv("AO_PLAN_ID", "plan_env")
	t.Setenv("AO_SESSION_ID", "")
	reqs, _, err := runPlanCLI(t, "plan", "milestone", "add", "--key", "m1", "--title", "A", "--description", "do A")
	if err != nil {
		t.Fatal(err)
	}
	if len(reqs) != 1 || reqs[0].Path != "/api/v1/plans/plan_env/milestones" {
		t.Fatalf("requests = %+v", reqs)
	}
	reqs, _, err = runPlanCLI(t, "plan", "activate")
	if err != nil || reqs[0].Path != "/api/v1/plans/plan_env/activate" {
		t.Fatalf("activate requests = %+v err=%v", reqs, err)
	}
}

func TestPlanQAResultUsesSessionEnv(t *testing.T) {
	t.Setenv("AO_PLAN_ID", "")
	t.Setenv("AO_SESSION_ID", "qa-1")
	reqs, _, err := runPlanCLI(t, "plan", "qa-result", "plan_1", "--fail", "--milestone", "m2", "--note", "broken")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"verdict": "fail", "milestoneKey": "m2", "note": "broken", "sessionId": "qa-1"}
	gotJSON, _ := json.Marshal(reqs[0].Body)
	wantJSON, _ := json.Marshal(want)
	if reqs[0].Path != "/api/v1/plans/plan_1/qa-result" || string(gotJSON) != string(wantJSON) {
		t.Fatalf("request = %s %s", reqs[0].Path, gotJSON)
	}
}

func TestPlanUsageErrors(t *testing.T) {
	cases := [][]string{
		{"plan", "create", "--project", "demo", "--brief", "b"},
		{"plan", "create", "--project", "demo", "--title", "t"},
		{"plan", "create", "--project", "demo", "--title", "t", "--brief", "b", "--brief-file", "x"},
		{"plan", "get"},
		{"plan", "set-spec", "plan_1"},
		{"plan", "milestone", "add", "plan_1", "--title", "t", "--description", "d"},
		{"plan", "milestone", "add", "plan_1", "--key", "m1", "--description", "d"},
		{"plan", "milestone", "add", "plan_1", "--key", "m1", "--title", "t", "--kind", "deploy", "--description", "d"},
		{"plan", "milestone", "update", "plan_1", "m1"},
		{"plan", "milestone", "rm", "plan_1"},
		{"plan", "qa-result", "plan_1", "--note", "n"},
		{"plan", "qa-result", "plan_1", "--pass", "--fail", "--note", "n"},
		{"plan", "qa-result", "plan_1", "--fail", "--note", "n"},
		{"plan", "qa-result", "plan_1", "--pass"},
		{"plan", "cancel", "plan_1"},
		{"plan", "events", "plan_1", "--after", "-1"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args[1:], " "), func(t *testing.T) {
			t.Setenv("AO_PLAN_ID", "")
			t.Setenv("AO_SESSION_ID", "qa-1")
			reqs, _, err := runPlanCLI(t, args...)
			if err == nil || ExitCode(err) != 2 {
				t.Fatalf("err=%v exit=%d", err, ExitCode(err))
			}
			if len(reqs) != 0 {
				t.Fatalf("usage error still called the daemon: %+v", reqs)
			}
		})
	}
}

func TestPlanQAResultRequiresSession(t *testing.T) {
	t.Setenv("AO_PLAN_ID", "")
	t.Setenv("AO_SESSION_ID", "")
	_, _, err := runPlanCLI(t, "plan", "qa-result", "plan_1", "--pass", "--note", "ok")
	if err == nil || ExitCode(err) != 2 || !strings.Contains(err.Error(), "AO_SESSION_ID") {
		t.Fatalf("err=%v", err)
	}
}

func TestPlanCommandKeepsDaemonErrorEnvelope(t *testing.T) {
	t.Setenv("AO_PLAN_ID", "")
	cfg := setConfigEnv(t)
	_, server := newPlanServer(t, map[string]struct {
		status int
		body   string
	}{
		"POST /api/v1/plans/plan_1/activate": {http.StatusBadRequest,
			`{"error":"bad_request","code":"PLAN_GRAPH_INVALID","message":"dependency cycle: m1 -> m2 -> m1","requestId":"request-9"}`},
	})
	writeRunFileFor(t, cfg, server)
	_, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "plan", "activate", "plan_1")
	if err == nil || ExitCode(err) != 1 || !strings.Contains(err.Error(), "PLAN_GRAPH_INVALID") ||
		!strings.Contains(err.Error(), "dependency cycle") || !strings.Contains(err.Error(), "request-9") {
		t.Fatalf("err=%v exit=%d", err, ExitCode(err))
	}
}

func TestPlanListJSON(t *testing.T) {
	t.Setenv("AO_PLAN_ID", "")
	_, out, err := runPlanCLI(t, "plan", "ls", "--project", "demo", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Plans []struct{ ID string } `json:"plans"`
	}
	if err := json.Unmarshal([]byte(out), &decoded); err != nil || len(decoded.Plans) != 1 || decoded.Plans[0].ID != "plan_1" {
		t.Fatalf("json output %q err=%v", out, err)
	}
}
