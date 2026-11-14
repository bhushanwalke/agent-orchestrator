package plan_test

import (
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/plan"
)

func planQAPass(session domain.SessionID) plan.QAResultInput {
	return plan.QAResultInput{Verdict: domain.QAVerdictPass, Note: "verified", SessionID: session}
}

func planQAFail(session domain.SessionID, key, note string) plan.QAResultInput {
	return plan.QAResultInput{Verdict: domain.QAVerdictFail, MilestoneKey: key, Note: note, SessionID: session}
}
