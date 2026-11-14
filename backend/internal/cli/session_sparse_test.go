package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSessionDTOCarriesSparsePathsAndPlan(t *testing.T) {
	var resp sessionResponse
	if err := json.Unmarshal([]byte(`{"session":{"id":"sp-1","sparsePaths":["a","c"],"planId":"plan_1","planMilestoneId":"ms_1"}}`), &resp); err != nil {
		t.Fatal(err)
	}
	out, _ := json.Marshal(resp)
	for _, want := range []string{`"sparsePaths":["a","c"]`, `"planId":"plan_1"`, `"planMilestoneId":"ms_1"`} {
		if !strings.Contains(string(out), want) {
			t.Fatalf("ao session get --json would drop %s: %s", want, out)
		}
	}
}
