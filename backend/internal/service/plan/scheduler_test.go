package plan

import (
	"reflect"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestEffectiveSparsePaths(t *testing.T) {
	enabled := domain.ProjectRecord{Config: domain.ProjectConfig{SparseCheckout: domain.SparseCheckoutConfig{Enabled: true}}}
	disabled := domain.ProjectRecord{}
	sparse := domain.Milestone{Kind: domain.MilestoneKindImplement, SparsePaths: []string{"src/api"}}
	full := domain.Milestone{Kind: domain.MilestoneKindImplement}
	qa := domain.Milestone{Kind: domain.MilestoneKindQA, SparsePaths: []string{"src"}}

	if got, fellBack := effectiveSparsePaths(enabled, sparse); !reflect.DeepEqual(got, []string{"src/api"}) || fellBack {
		t.Fatalf("enabled sparse = %#v fellBack=%v", got, fellBack)
	}
	if got, fellBack := effectiveSparsePaths(disabled, sparse); got != nil || !fellBack {
		t.Fatalf("disabled project = %#v fellBack=%v, want full checkout with sparse_disabled", got, fellBack)
	}
	if got, fellBack := effectiveSparsePaths(disabled, full); got != nil || fellBack {
		t.Fatalf("no sparse paths = %#v fellBack=%v", got, fellBack)
	}
	if got, fellBack := effectiveSparsePaths(enabled, qa); got != nil || fellBack {
		t.Fatalf("qa always gets a full checkout, got %#v fellBack=%v", got, fellBack)
	}
}
