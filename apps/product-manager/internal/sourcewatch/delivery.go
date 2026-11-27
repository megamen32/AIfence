package sourcewatch

import (
	"context"

	"github.com/airlockrun/agentsdk"
)

// One occurrence keeps its identity even when a source changes between
// delivery attempts. Pinned input is reused before any fresh source reads.
func occurrenceSignalID(owner string, in JobInput) string {
	return ID(owner, "signal", in)
}

func checkOrResume(ctx context.Context, w Window, approved bool, read Read, launch Launch, pinned *ReviewInput) (Outcome, error) {
	if err := ctx.Err(); err != nil {
		return Outcome{}, err
	}
	if pinned == nil {
		return Check(ctx, w, approved, read, launch)
	}
	if !approved {
		return Outcome{Status: "model_route_unapproved", Warnings: pinned.Signal.SourceWarnings}, nil
	}
	runID, err := launch(ctx, pinned.Signal)
	if err != nil {
		return Outcome{}, err
	}
	return Outcome{Status: "submitted", AnalysisRunID: runID, Warnings: pinned.Signal.SourceWarnings}, nil
}

// Invalid evidence is corrected inside the native task before a completed
// answer is published. The original signal, not model-supplied IDs, is trusted.
func reviewSubmission(ctx context.Context, _ agentsdk.AgentReviewContext, submission agentsdk.AgentSubmission[ReviewInput, ReviewOutput]) (agentsdk.AgentReviewDecision, error) {
	if err := ctx.Err(); err != nil {
		return agentsdk.AgentReviewDecision{}, err
	}
	if _, err := ValidateReview(submission.Output, ReviewSignalIDs(submission.Input.Signal)); err != nil {
		return agentsdk.Continue("Correct the typed answer using only the original signal evidence. " + err.Error()), nil
	}
	return agentsdk.Accept(), nil
}
