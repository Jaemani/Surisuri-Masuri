package ingest

import (
	"errors"
	"testing"
	"time"
)

func TestCleanupExecutionProgressCommandBuildsBoundedMonotonicSteps(t *testing.T) {
	now, plan := cleanupLedgerPlanFixture(t, ArtifactClassificationValidComplete)
	ledger, err := NewCleanupExecutionLedger(plan)
	if err != nil {
		t.Fatalf("NewCleanupExecutionLedger() = %v", err)
	}
	steps := []struct {
		phase         CleanupExecutionPhase
		deleteOutcome CleanupDeleteRPCOutcome
		auditOutcome  CleanupAuditOutcome
	}{
		{phase: CleanupExecutionPhaseRawDispatchRecorded},
		{phase: CleanupExecutionPhaseRawOutcomeRecorded, deleteOutcome: CleanupDeleteObserved},
		{phase: CleanupExecutionPhaseRawAbsenceConfirmed, auditOutcome: CleanupAuditConfirmedAbsent},
		{phase: CleanupExecutionPhaseManifestDispatchRecorded},
		{phase: CleanupExecutionPhaseManifestOutcomeRecorded, deleteOutcome: CleanupDeleteNotFound},
		{phase: CleanupExecutionPhaseManifestAbsenceConfirmed, auditOutcome: CleanupAuditConfirmedAbsent},
	}
	for index, step := range steps {
		command, buildErr := BuildCleanupExecutionProgressCommand(
			plan, ledger, step.phase, step.deleteOutcome, step.auditOutcome,
		)
		if buildErr != nil || ValidateCleanupExecutionProgressCommand(command) != nil {
			t.Fatalf("step %d command = %#v, %v", index, command, buildErr)
		}
		if command.ExpectedLedgerRevision != ledger.Revision ||
			command.ExpectedTargetHash != plan.Target.TargetHash || command.ExpectedPlanHash != plan.PlanHash {
			t.Fatalf("step %d command binding = %#v", index, command)
		}
		next, advanceErr := AdvanceCleanupExecutionLedger(plan, ledger, CleanupExecutionTransition{
			Phase: step.phase, DeleteOutcome: step.deleteOutcome, AuditOutcome: step.auditOutcome,
			ObservedAt: now.Add(time.Duration(index+1) * time.Second),
		})
		if advanceErr != nil {
			t.Fatalf("step %d advance = %v", index, advanceErr)
		}
		if !CleanupExecutionProgressAlreadyApplied(plan, next, command) {
			t.Fatalf("step %d was not recognized as exact replay", index)
		}
		ledger = next
	}
}

func TestCleanupExecutionProgressCommandRejectsSkipCompletionAndTamper(t *testing.T) {
	_, plan := cleanupLedgerPlanFixture(t, ArtifactClassificationValidComplete)
	ledger, err := NewCleanupExecutionLedger(plan)
	if err != nil {
		t.Fatalf("NewCleanupExecutionLedger() = %v", err)
	}
	if _, err := BuildCleanupExecutionProgressCommand(
		plan, ledger, CleanupExecutionPhaseRawOutcomeRecorded, CleanupDeleteObserved, "",
	); !errors.Is(err, ErrInvalidCleanupExecutionLedger) {
		t.Fatalf("skipped command error = %v", err)
	}
	if _, err := BuildCleanupExecutionProgressCommand(
		plan, ledger, CleanupExecutionPhaseCompleted, "", "",
	); !errors.Is(err, ErrInvalidCleanupExecutionLedger) {
		t.Fatalf("completion command error = %v", err)
	}

	command, err := BuildCleanupExecutionProgressCommand(
		plan, ledger, CleanupExecutionPhaseRawDispatchRecorded, "", "",
	)
	if err != nil {
		t.Fatalf("BuildCleanupExecutionProgressCommand() = %v", err)
	}
	command.ExpectedLedgerRevision = 0
	if !errors.Is(
		ValidateCleanupExecutionProgressCommand(command),
		ErrInvalidCleanupExecutionLedger,
	) {
		t.Fatal("zero expected revision remained valid")
	}
	forged := CleanupExecutionProgressCommand{
		Query:              cleanupExecutionQueryFixtureForPlan(plan),
		ExpectedTargetHash: plan.Target.TargetHash, ExpectedPlanHash: plan.PlanHash,
		ExpectedReceiptRevision: plan.Target.Command.ReceiptRevision,
		ExpectedLedgerRevision:  1, Phase: CleanupExecutionPhaseManifestDispatchRecorded,
	}
	if !errors.Is(
		ValidateCleanupExecutionProgressCommand(forged),
		ErrInvalidCleanupExecutionLedger,
	) {
		t.Fatal("direct skipped phase command remained valid")
	}
}

func TestCleanupExecutionProgressReplayRequiresExactRevisionAndSemantics(t *testing.T) {
	now, plan := cleanupLedgerPlanFixture(t, ArtifactClassificationValidComplete)
	ledger, err := NewCleanupExecutionLedger(plan)
	if err != nil {
		t.Fatalf("NewCleanupExecutionLedger() = %v", err)
	}
	dispatchCommand, err := BuildCleanupExecutionProgressCommand(
		plan, ledger, CleanupExecutionPhaseRawDispatchRecorded, "", "",
	)
	if err != nil {
		t.Fatalf("dispatch command = %v", err)
	}
	ledger, err = AdvanceCleanupExecutionLedger(plan, ledger, CleanupExecutionTransition{
		Phase: CleanupExecutionPhaseRawDispatchRecorded, ObservedAt: now.Add(time.Second),
	})
	if err != nil {
		t.Fatalf("dispatch advance = %v", err)
	}
	if !CleanupExecutionProgressAlreadyApplied(plan, ledger, dispatchCommand) {
		t.Fatal("exact dispatch was not recognized")
	}
	tampered := dispatchCommand
	tampered.ExpectedPlanHash = differentCleanupExecutionDigest(tampered.ExpectedPlanHash)
	if CleanupExecutionProgressAlreadyApplied(plan, ledger, tampered) {
		t.Fatal("different plan hash was recognized as replay")
	}

	outcomeCommand, err := BuildCleanupExecutionProgressCommand(
		plan, ledger, CleanupExecutionPhaseRawOutcomeRecorded, CleanupDeleteObserved, "",
	)
	if err != nil {
		t.Fatalf("outcome command = %v", err)
	}
	ledger, err = AdvanceCleanupExecutionLedger(plan, ledger, CleanupExecutionTransition{
		Phase: CleanupExecutionPhaseRawOutcomeRecorded, DeleteOutcome: CleanupDeleteNotFound,
		ObservedAt: now.Add(2 * time.Second),
	})
	if err != nil {
		t.Fatalf("outcome advance = %v", err)
	}
	if CleanupExecutionProgressAlreadyApplied(plan, ledger, outcomeCommand) {
		t.Fatal("different delete outcome was recognized as replay")
	}
	matchingOutcome := outcomeCommand
	matchingOutcome.DeleteOutcome = CleanupDeleteNotFound
	if !CleanupExecutionProgressAlreadyApplied(plan, ledger, matchingOutcome) {
		t.Fatal("exact delete outcome was not recognized as replay")
	}
	malformed := ledger
	malformed.Raw.OutcomeRecordedAt = time.Time{}
	if CleanupExecutionProgressAlreadyApplied(plan, malformed, matchingOutcome) {
		t.Fatal("malformed durable ledger was recognized as replay")
	}
	foreign := matchingOutcome
	foreign.Query.AttemptID = "99999999-9999-4999-8999-999999999999"
	if CleanupExecutionProgressAlreadyApplied(plan, ledger, foreign) {
		t.Fatal("foreign query was recognized as replay")
	}
}

func cleanupExecutionQueryFixtureForPlan(plan CleanupExecutionLedgerPlan) CleanupExecutionQuery {
	return CleanupExecutionQuery{
		TenantID:       plan.Target.Command.TenantID,
		ReservationKey: plan.Target.Command.ReservationKey,
		AttemptID:      plan.Target.Command.AttemptID,
	}
}
