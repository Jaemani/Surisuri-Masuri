package ingest

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/telemetry"
)

const cleanupExecutionPlanBindingVersion = "cleanup-execution-plan@1"

var (
	ErrCleanupExecutionUnauthorized       = errors.New("cleanup execution is unauthorized")
	ErrCleanupExecutionUnavailable        = errors.New("cleanup execution is unavailable")
	ErrCleanupExecutionGenerationDrift    = errors.New("cleanup execution generation drift")
	ErrCleanupExecutionLineageMismatch    = errors.New("cleanup execution lineage mismatch")
	ErrInvalidCleanupExecutionObservation = errors.New("cleanup execution observation is invalid")
)

type CleanupExecutionQuery struct {
	TenantID       string
	ReservationKey string
	AttemptID      string
}

type CurrentCleanupExecutionSnapshot struct {
	Receipt  Receipt
	Attempt  CurrentCleanupAttempt
	Target   CleanupTarget
	ReadTime time.Time
}

type CleanupExecutionPlan struct {
	Target               CleanupTarget
	ExpectedRawPath      string
	ExpectedManifestPath string
}

type CleanupDeleteRPCOutcome string

const (
	CleanupDeleteNotAttempted CleanupDeleteRPCOutcome = "not_attempted"
	CleanupDeleteObserved     CleanupDeleteRPCOutcome = "deleted_observed"
	CleanupDeleteNotFound     CleanupDeleteRPCOutcome = "not_found_observed"
	CleanupDeleteUnknown      CleanupDeleteRPCOutcome = "unknown"
)

type CleanupAuditOutcome string

const (
	CleanupAuditConfirmedAbsent CleanupAuditOutcome = "confirmed_absent"
)

type CleanupArtifactExecutionObservation struct {
	DeleteOutcome CleanupDeleteRPCOutcome
	AuditOutcome  CleanupAuditOutcome
}

// CleanupExecutionObservation is a non-authoritative, in-process observation.
// It is not a capability and must not be accepted by a receipt/attempt
// finalizer without a separate fresh fenced persistence contract.
type CleanupExecutionObservation struct {
	PlanHash    string
	TargetHash  string
	Raw         CleanupArtifactExecutionObservation
	Manifest    CleanupArtifactExecutionObservation
	CompletedAt time.Time
}

func BuildCleanupExecutionPlan(
	query CleanupExecutionQuery,
	snapshot CurrentCleanupExecutionSnapshot,
	checkedAt time.Time,
) (CleanupExecutionPlan, error) {
	if ValidateCleanupExecutionQuery(query) != nil || snapshot.ReadTime.IsZero() || checkedAt.IsZero() {
		return CleanupExecutionPlan{}, ErrCleanupExecutionUnauthorized
	}
	target, err := CloneCleanupTarget(snapshot.Target)
	if err != nil || target.Command.Status != CleanupTargetStatusPlanned ||
		target.Command.Decision != CleanupTargetDeleteCandidate ||
		target.Command.TenantID != query.TenantID ||
		target.Command.ReservationKey != query.ReservationKey ||
		target.Command.AttemptID != query.AttemptID ||
		target.Command.CleanupID != query.AttemptID ||
		target.Command.ReceiptID != snapshot.Receipt.ReceiptID ||
		target.Command.CreatedAt.After(checkedAt) {
		return CleanupExecutionPlan{}, ErrCleanupExecutionUnauthorized
	}
	lease := CleanupLeaseGrant{
		Lease: LeaseGrant{
			Fence: LeaseFence{
				OwnerID:   target.Command.AttemptID,
				Token:     target.Command.FencingToken,
				ExpiresAt: target.Command.LeaseExpiresAt,
			},
			OwnerKind:   LeaseOwnerCleanup,
			AcquiredAt:  target.Command.LeaseAcquiredAt,
			HeartbeatAt: target.Command.LeaseHeartbeatAt,
		},
		ReceiptRevision: target.Command.ReceiptRevision,
		Mode:            target.Command.Mode,
		OriginStatus:    target.Command.OriginStatus,
		PolicyVersion:   target.Command.CleanupPolicyVersion,
		TransitionedAt:  target.Command.CleanupTransitionedAt,
		QuiescenceUntil: target.Command.CleanupQuiescenceUntil,
	}
	cleanupSnapshot := CurrentCleanupSnapshot{
		Receipt:  snapshot.Receipt,
		Attempt:  snapshot.Attempt,
		ReadTime: snapshot.ReadTime,
	}
	if evaluateCurrentCleanup(
		cleanupSnapshot,
		query.TenantID,
		query.ReservationKey,
		lease,
		checkedAt,
	) != nil {
		return CleanupExecutionPlan{}, ErrCleanupExecutionUnauthorized
	}
	request, err := cleanupClassificationRequest(snapshot.Receipt)
	if err != nil {
		return CleanupExecutionPlan{}, ErrCleanupExecutionUnauthorized
	}
	plan := CleanupExecutionPlan{
		Target:               target,
		ExpectedRawPath:      request.ExpectedRawPath,
		ExpectedManifestPath: request.ExpectedManifestPath,
	}
	if ValidateCleanupExecutionPlan(plan) != nil {
		return CleanupExecutionPlan{}, ErrCleanupExecutionUnauthorized
	}
	return plan, nil
}

func ValidateCleanupExecutionQuery(query CleanupExecutionQuery) error {
	if !telemetry.IsUUID(query.TenantID) || !isLowerHexDigest(query.ReservationKey) ||
		!telemetry.IsUUID(query.AttemptID) {
		return ErrCleanupExecutionUnavailable
	}
	return nil
}

func ValidateCleanupExecutionPlan(plan CleanupExecutionPlan) error {
	target, err := CloneCleanupTarget(plan.Target)
	if err != nil || plan.ExpectedRawPath == "" || plan.ExpectedManifestPath == "" ||
		plan.ExpectedRawPath == plan.ExpectedManifestPath ||
		target.Command.Status != CleanupTargetStatusPlanned ||
		target.Command.Decision != CleanupTargetDeleteCandidate {
		return ErrCleanupExecutionUnauthorized
	}
	if target.Command.Raw != nil && target.Command.Raw.Path != plan.ExpectedRawPath {
		return ErrCleanupExecutionUnauthorized
	}
	if target.Command.Manifest != nil && target.Command.Manifest.Path != plan.ExpectedManifestPath {
		return ErrCleanupExecutionUnauthorized
	}
	return nil
}

func CloneCleanupExecutionPlan(plan CleanupExecutionPlan) (CleanupExecutionPlan, error) {
	target, err := CloneCleanupTarget(plan.Target)
	if err != nil {
		return CleanupExecutionPlan{}, err
	}
	cloned := CleanupExecutionPlan{
		Target:               target,
		ExpectedRawPath:      plan.ExpectedRawPath,
		ExpectedManifestPath: plan.ExpectedManifestPath,
	}
	if ValidateCleanupExecutionPlan(cloned) != nil {
		return CleanupExecutionPlan{}, ErrCleanupExecutionUnauthorized
	}
	return cloned, nil
}

func CloneCleanupTarget(target CleanupTarget) (CleanupTarget, error) {
	command := cloneCleanupTargetCommand(target.Command)
	if ValidateCleanupTargetCommand(command) != nil || !isLowerHexDigest(target.TargetHash) {
		return CleanupTarget{}, ErrInvalidCleanupTarget
	}
	hash, err := CleanupTargetHash(command)
	if err != nil || hash != target.TargetHash {
		return CleanupTarget{}, ErrCleanupTargetConflict
	}
	return CleanupTarget{Command: command, TargetHash: target.TargetHash}, nil
}

func CleanupExecutionPlanHash(plan CleanupExecutionPlan) (string, error) {
	plan, err := CloneCleanupExecutionPlan(plan)
	if err != nil {
		return "", err
	}
	sum := canonicalCleanupExecutionPlanBinding(plan)
	return hex.EncodeToString(sum[:]), nil
}

// ValidateCleanupExecutionObservationShape checks internal consistency only.
// A valid shape is neither a capability nor durable completion evidence.
func ValidateCleanupExecutionObservationShape(
	plan CleanupExecutionPlan,
	observation CleanupExecutionObservation,
) error {
	plan, err := CloneCleanupExecutionPlan(plan)
	if err != nil || observation.CompletedAt.IsZero() || !isLowerHexDigest(observation.PlanHash) ||
		observation.TargetHash != plan.Target.TargetHash {
		return ErrInvalidCleanupExecutionObservation
	}
	planHash, err := CleanupExecutionPlanHash(plan)
	if err != nil || observation.PlanHash != planHash ||
		validateCleanupArtifactObservation(plan.Target.Command.Raw != nil, observation.Raw) != nil ||
		validateCleanupArtifactObservation(plan.Target.Command.Manifest != nil, observation.Manifest) != nil {
		return ErrInvalidCleanupExecutionObservation
	}
	return nil
}

func validateCleanupArtifactObservation(
	targeted bool,
	observation CleanupArtifactExecutionObservation,
) error {
	if observation.AuditOutcome != CleanupAuditConfirmedAbsent {
		return ErrInvalidCleanupExecutionObservation
	}
	if targeted {
		switch observation.DeleteOutcome {
		case CleanupDeleteNotAttempted,
			CleanupDeleteObserved,
			CleanupDeleteNotFound:
			return nil
		default:
			return ErrInvalidCleanupExecutionObservation
		}
	}
	if observation.DeleteOutcome != CleanupDeleteNotAttempted {
		return ErrInvalidCleanupExecutionObservation
	}
	return nil
}

func canonicalCleanupExecutionPlanBinding(plan CleanupExecutionPlan) [sha256.Size]byte {
	encoder := newArtifactBindingEncoder(cleanupExecutionPlanBindingVersion)
	targetBinding := canonicalCleanupTargetBinding(plan.Target.Command)
	encoder.addBytes(targetBinding[:])
	encoder.addString(plan.Target.TargetHash)
	encoder.addString(plan.ExpectedRawPath)
	encoder.addString(plan.ExpectedManifestPath)
	return encoder.sum()
}
