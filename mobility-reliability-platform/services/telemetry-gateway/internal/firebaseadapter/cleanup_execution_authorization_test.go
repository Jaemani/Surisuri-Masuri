package firebaseadapter

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
)

func TestCleanupExecutionAuthorizationGrantRejectsZeroAndForgedCapabilities(t *testing.T) {
	checkedAt, plan, grant := cleanupExecutionAuthorizationFixture(t, time.Minute)
	if err := ValidateCleanupExecutionAuthorization(grant, plan, checkedAt); err != nil {
		t.Fatalf("valid grant = %v", err)
	}
	if err := ValidateCleanupExecutionAuthorization(
		CleanupExecutionAuthorizationGrant{}, plan, checkedAt,
	); !errors.Is(err, ingest.ErrCleanupExecutionUnauthorized) {
		t.Fatalf("zero grant = %v", err)
	}

	tests := []struct {
		name   string
		reseal bool
		mutate func(*CleanupExecutionAuthorizationGrant)
	}{
		{name: "policy", reseal: true, mutate: func(value *CleanupExecutionAuthorizationGrant) {
			value.policyVersion = "cleanup-execution.forged@1"
		}},
		{name: "checked at", mutate: func(value *CleanupExecutionAuthorizationGrant) {
			value.checkedAt = value.checkedAt.Add(time.Nanosecond)
		}},
		{name: "expiry", mutate: func(value *CleanupExecutionAuthorizationGrant) {
			value.expiresAt = value.expiresAt.Add(time.Second)
		}},
		{name: "receipt revision", reseal: true, mutate: func(value *CleanupExecutionAuthorizationGrant) {
			value.receiptRevision++
		}},
		{name: "owner", reseal: true, mutate: func(value *CleanupExecutionAuthorizationGrant) {
			value.ownerID = "88888888-8888-4888-8888-888888888888"
		}},
		{name: "fencing token", reseal: true, mutate: func(value *CleanupExecutionAuthorizationGrant) {
			value.fencingToken++
		}},
		{name: "lease expiry", reseal: true, mutate: func(value *CleanupExecutionAuthorizationGrant) {
			value.leaseExpiresAt = value.leaseExpiresAt.Add(time.Second)
		}},
		{name: "plan hash", reseal: true, mutate: func(value *CleanupExecutionAuthorizationGrant) {
			value.planHash = strings.Repeat("f", 64)
		}},
		{name: "capability seal", mutate: func(value *CleanupExecutionAuthorizationGrant) {
			value.capabilitySeal[0] ^= 0xff
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			forged := grant
			test.mutate(&forged)
			if test.reseal {
				forged.capabilitySeal = cleanupExecutionCapabilitySeal(forged)
			}
			if err := ValidateCleanupExecutionAuthorization(
				forged, plan, checkedAt,
			); !errors.Is(err, ingest.ErrCleanupExecutionUnauthorized) {
				t.Fatalf("forged grant = %v", err)
			}
		})
	}
}

func TestCleanupExecutionAuthorizationUsesExclusiveExpiryAndConservativeClock(t *testing.T) {
	checkedAt, plan, grant := cleanupExecutionAuthorizationFixture(t, time.Minute)
	if err := ValidateCleanupExecutionAuthorization(
		grant, plan, grant.expiresAt.Add(-time.Nanosecond),
	); err != nil {
		t.Fatalf("just before expiry = %v", err)
	}
	if err := ValidateCleanupExecutionAuthorization(
		grant, plan, grant.expiresAt,
	); !errors.Is(err, ErrCleanupExecutionAuthorizationExpired) {
		t.Fatalf("exact expiry = %v", err)
	}
	if err := ValidateCleanupExecutionAuthorization(
		grant, plan, checkedAt.Add(-maxAdmissionClockSkew),
	); err != nil {
		t.Fatalf("bounded application lag = %v", err)
	}
	if err := ValidateCleanupExecutionAuthorization(
		grant, plan, checkedAt.Add(-maxAdmissionClockSkew-time.Nanosecond),
	); !errors.Is(err, ingest.ErrCleanupExecutionUnavailable) {
		t.Fatalf("excessive application lag = %v", err)
	}
}

func TestCleanupExecutionAuthorizationBindsPlanAndDeadline(t *testing.T) {
	checkedAt, plan, grant := cleanupExecutionAuthorizationFixture(t, 10*time.Second)
	deadline, err := CleanupExecutionAuthorizationDeadline(grant, plan)
	if err != nil || !deadline.Equal(checkedAt.Add(10*time.Second)) {
		t.Fatalf("lease-capped deadline = %s, %v", deadline, err)
	}

	tampered, err := ingest.CloneCleanupExecutionPlan(plan)
	if err != nil {
		t.Fatalf("CloneCleanupExecutionPlan() = %v", err)
	}
	tampered.ExpectedManifestPath += ".late"
	if err := ValidateCleanupExecutionAuthorization(
		grant, tampered, checkedAt,
	); !errors.Is(err, ingest.ErrCleanupExecutionUnauthorized) {
		t.Fatalf("substituted plan = %v", err)
	}
	if _, err := CleanupExecutionAuthorizationDeadline(grant, tampered); !errors.Is(
		err, ingest.ErrCleanupExecutionUnauthorized,
	) {
		t.Fatalf("substituted plan deadline = %v", err)
	}
}

func cleanupExecutionAuthorizationFixture(
	t *testing.T,
	leaseRemaining time.Duration,
) (time.Time, ingest.CleanupExecutionPlan, CleanupExecutionAuthorizationGrant) {
	t.Helper()
	checkedAt := time.Date(2026, time.July, 22, 13, 0, 0, 0, time.UTC)
	leaseExpiresAt := checkedAt.Add(leaseRemaining)
	raw := &ingest.ArtifactLineage{
		Path: "telemetry/tenant/raw.json.gz", SHA256: strings.Repeat("b", 64),
		CRC32C: 123, Size: 4096, Generation: 1700000000000001, Metageneration: 1,
	}
	command := ingest.CleanupTargetCommand{
		SchemaVersion:          ingest.CleanupTargetSchemaVersion,
		CleanupID:              "77777777-7777-4777-8777-777777777777",
		TenantID:               "11111111-1111-4111-8111-111111111111",
		ReceiptID:              "01982015-4400-7000-8000-000000000001",
		ReservationKey:         strings.Repeat("a", 64),
		AttemptID:              "77777777-7777-4777-8777-777777777777",
		Mode:                   ingest.CleanupModeReservationExpiry,
		OriginStatus:           ingest.ReceiptReserved,
		CleanupPolicyVersion:   ingest.CleanupTransitionPolicyV1,
		CleanupTransitionedAt:  checkedAt.Add(-15 * time.Minute),
		CleanupQuiescenceUntil: checkedAt.Add(-4 * time.Minute),
		ReceiptRevision:        5,
		FencingToken:           4,
		LeaseAcquiredAt:        checkedAt.Add(-3 * time.Minute),
		LeaseHeartbeatAt:       checkedAt.Add(-time.Minute),
		LeaseExpiresAt:         leaseExpiresAt,
		WorkerVersion:          ingest.CleanupWorkerVersion,
		Status:                 ingest.CleanupTargetStatusPlanned,
		Decision:               ingest.CleanupTargetDeleteCandidate,
		Classification:         ingest.ArtifactClassificationValidRawOnly,
		ReasonCode:             ingest.ArtifactReasonRawValidManifestAbsent,
		RetentionPhase:         ingest.ArtifactRetentionBeforeExpiry,
		ValidatorVersion:       ingest.TelemetryValidatorVersion,
		ClassifiedAt:           checkedAt.Add(-45 * time.Second),
		RawInventory: ingest.ArtifactInventorySummary{
			Performed: true, NonSoftDeletedCount: 1,
			Coverage: ingest.ArtifactInventoryCoverageComplete,
		},
		ManifestInventory: ingest.ArtifactInventorySummary{
			Performed: true, Coverage: ingest.ArtifactInventoryCoverageComplete,
		},
		Raw:       raw,
		CreatedAt: checkedAt.Add(-30 * time.Second),
	}
	targetHash, err := ingest.CleanupTargetHash(command)
	if err != nil {
		t.Fatalf("CleanupTargetHash() = %v", err)
	}
	plan := ingest.CleanupExecutionPlan{
		Target:               ingest.CleanupTarget{Command: command, TargetHash: targetHash},
		ExpectedRawPath:      raw.Path,
		ExpectedManifestPath: "telemetry/tenant/manifest.json",
	}
	planHash, err := ingest.CleanupExecutionPlanHash(plan)
	if err != nil {
		t.Fatalf("CleanupExecutionPlanHash() = %v", err)
	}
	expiresAt := checkedAt.Add(CleanupExecutionGrantTTL)
	if leaseExpiresAt.Before(expiresAt) {
		expiresAt = leaseExpiresAt
	}
	grant := CleanupExecutionAuthorizationGrant{
		policyVersion:   CleanupExecutionPolicyVersion,
		checkedAt:       checkedAt,
		expiresAt:       expiresAt,
		receiptRevision: command.ReceiptRevision,
		ownerID:         command.AttemptID,
		fencingToken:    command.FencingToken,
		leaseExpiresAt:  command.LeaseExpiresAt,
		planHash:        planHash,
	}
	grant.capabilitySeal = cleanupExecutionCapabilitySeal(grant)
	return checkedAt, plan, grant
}
