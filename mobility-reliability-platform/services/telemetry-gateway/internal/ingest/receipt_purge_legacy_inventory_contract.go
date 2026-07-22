package ingest

import (
	"context"
	"errors"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/telemetry"
)

const MaxReceiptPurgeLegacyInventoryPageSize = 25

var (
	ErrInvalidReceiptPurgeLegacyInventory     = errors.New("receipt purge legacy inventory is invalid")
	ErrReceiptPurgeLegacyInventoryConflict    = errors.New("receipt purge legacy inventory conflicts with current state")
	ErrReceiptPurgeLegacyInventoryUnavailable = errors.New("receipt purge legacy inventory is unavailable")
	ErrReceiptPurgeLegacyFindingUnsupported   = errors.New("legacy integrity finding inventory is unsupported")
)

type ReceiptPurgeLegacyInventoryRequest struct {
	TenantID string
	Cursor   string
	PageSize int
}

// ReceiptPurgeLegacyInventoryPage is an advisory, tenant-scoped page. It is
// not a point-in-time snapshot and must be re-read inside the transaction that
// creates any missing inverse links.
type ReceiptPurgeLegacyInventoryPage struct {
	Request             ReceiptPurgeLegacyInventoryRequest
	DocumentIDs         []string
	LookaheadDocumentID string
	NextCursor          string
	ObservedExhausted   bool
}

type ReceiptPurgeLegacyTargetStatus string

const (
	ReceiptPurgeLegacyTargetRegistered         ReceiptPurgeLegacyTargetStatus = "registered"
	ReceiptPurgeLegacyTargetUnregistered       ReceiptPurgeLegacyTargetStatus = "unregistered"
	ReceiptPurgeLegacyTargetMalformedChild     ReceiptPurgeLegacyTargetStatus = "malformed_child"
	ReceiptPurgeLegacyTargetForeignChild       ReceiptPurgeLegacyTargetStatus = "foreign_child"
	ReceiptPurgeLegacyTargetLinkageDrift       ReceiptPurgeLegacyTargetStatus = "linkage_drift"
	ReceiptPurgeLegacyTargetFencedUnregistered ReceiptPurgeLegacyTargetStatus = "fenced_unregistered"
)

type ReceiptPurgeLegacyTargetObservation struct {
	DocumentID   string
	Status       ReceiptPurgeLegacyTargetStatus
	ExpectedLink ReceiptPurgeInverseLink
}

type ReceiptPurgeLegacyBackfillPlan struct {
	Page                      ReceiptPurgeLegacyInventoryPage
	LinksToCreate             []ReceiptPurgeInverseLink
	ObservedRegisteredCount   int
	ObservedUnregisteredCount int
	HeldDocumentID            string
	HeldStatus                ReceiptPurgeLegacyTargetStatus
	NextCursor                string
	ObservedExhausted         bool
}

type ReceiptPurgeLegacyFindingProbeStatus string

const (
	ReceiptPurgeLegacyFindingEmptyObserved ReceiptPurgeLegacyFindingProbeStatus = "empty_observed"
	ReceiptPurgeLegacyFindingUnsupported   ReceiptPurgeLegacyFindingProbeStatus = "unsupported"
)

// ReceiptPurgeLegacyInventoryStore exposes an operator-driven, tenant-scoped
// rollout gate. Exhaustion is only the result of one ordered observation; it
// is not global orphan-zero or writer-exclusion evidence.
type ReceiptPurgeLegacyInventoryStore interface {
	ListReceiptPurgeLegacyTargetPage(
		context.Context,
		ReceiptPurgeLegacyInventoryRequest,
	) (ReceiptPurgeLegacyInventoryPage, error)
	BackfillReceiptPurgeLegacyTargetPage(
		context.Context,
		ReceiptPurgeLegacyInventoryPage,
	) (ReceiptPurgeLegacyBackfillPlan, error)
}

func ValidateReceiptPurgeLegacyInventoryRequest(request ReceiptPurgeLegacyInventoryRequest) error {
	if !telemetry.IsUUID(request.TenantID) || request.PageSize < 1 ||
		request.PageSize > MaxReceiptPurgeLegacyInventoryPageSize ||
		(request.Cursor != "" && !telemetry.IsUUID(request.Cursor)) {
		return ErrInvalidReceiptPurgeLegacyInventory
	}
	return nil
}

// BuildReceiptPurgeLegacyInventoryPage consumes an ordered page_size+1 query
// result. The last identity is lookahead only and is never eligible for a
// write in this page.
func BuildReceiptPurgeLegacyInventoryPage(
	request ReceiptPurgeLegacyInventoryRequest,
	orderedDocumentIDs []string,
) (ReceiptPurgeLegacyInventoryPage, error) {
	if ValidateReceiptPurgeLegacyInventoryRequest(request) != nil ||
		len(orderedDocumentIDs) > request.PageSize+1 {
		return ReceiptPurgeLegacyInventoryPage{}, ErrInvalidReceiptPurgeLegacyInventory
	}
	previous := request.Cursor
	for _, documentID := range orderedDocumentIDs {
		if !validFirestoreDocumentID(documentID) || documentID <= previous {
			return ReceiptPurgeLegacyInventoryPage{}, ErrInvalidReceiptPurgeLegacyInventory
		}
		previous = documentID
	}
	pageCount := len(orderedDocumentIDs)
	lookahead := ""
	if pageCount > request.PageSize {
		pageCount = request.PageSize
		lookahead = orderedDocumentIDs[pageCount]
	}
	documentIDs := append([]string(nil), orderedDocumentIDs[:pageCount]...)
	nextCursor := request.Cursor
	if len(documentIDs) > 0 {
		nextCursor = documentIDs[len(documentIDs)-1]
	}
	return ReceiptPurgeLegacyInventoryPage{
		Request: request, DocumentIDs: documentIDs,
		LookaheadDocumentID: lookahead, NextCursor: nextCursor,
		ObservedExhausted: lookahead == "",
	}, nil
}

func SameReceiptPurgeLegacyInventoryPage(
	left ReceiptPurgeLegacyInventoryPage,
	right ReceiptPurgeLegacyInventoryPage,
) bool {
	if left.Request != right.Request || left.LookaheadDocumentID != right.LookaheadDocumentID ||
		left.NextCursor != right.NextCursor || left.ObservedExhausted != right.ObservedExhausted ||
		len(left.DocumentIDs) != len(right.DocumentIDs) {
		return false
	}
	for index := range left.DocumentIDs {
		if left.DocumentIDs[index] != right.DocumentIDs[index] {
			return false
		}
	}
	return true
}

func ValidateReceiptPurgeLegacyInventoryPage(page ReceiptPurgeLegacyInventoryPage) error {
	canonical, err := BuildReceiptPurgeLegacyInventoryPage(
		page.Request,
		append(append([]string(nil), page.DocumentIDs...), optionalLegacyLookahead(page)...),
	)
	if err != nil || !SameReceiptPurgeLegacyInventoryPage(page, canonical) {
		return ErrInvalidReceiptPurgeLegacyInventory
	}
	return nil
}

func PlanReceiptPurgeLegacyBackfill(
	page ReceiptPurgeLegacyInventoryPage,
	observations []ReceiptPurgeLegacyTargetObservation,
) (ReceiptPurgeLegacyBackfillPlan, error) {
	if ValidateReceiptPurgeLegacyInventoryPage(page) != nil || len(observations) != len(page.DocumentIDs) {
		return ReceiptPurgeLegacyBackfillPlan{}, ErrInvalidReceiptPurgeLegacyInventory
	}
	plan := ReceiptPurgeLegacyBackfillPlan{
		Page:              cloneReceiptPurgeLegacyInventoryPage(page),
		NextCursor:        page.NextCursor,
		ObservedExhausted: page.ObservedExhausted,
	}
	for index, observation := range observations {
		if observation.DocumentID != page.DocumentIDs[index] ||
			!validReceiptPurgeLegacyTargetStatus(observation.Status) {
			return ReceiptPurgeLegacyBackfillPlan{}, ErrInvalidReceiptPurgeLegacyInventory
		}
		switch observation.Status {
		case ReceiptPurgeLegacyTargetRegistered, ReceiptPurgeLegacyTargetUnregistered:
			if ValidateReceiptPurgeInverseLink(observation.ExpectedLink) != nil ||
				observation.ExpectedLink.TenantID != page.Request.TenantID ||
				observation.ExpectedLink.Kind != ReceiptPurgeLinkCleanupTarget ||
				observation.ExpectedLink.DocumentID != observation.DocumentID {
				return ReceiptPurgeLegacyBackfillPlan{}, ErrInvalidReceiptPurgeLegacyInventory
			}
			if observation.Status == ReceiptPurgeLegacyTargetRegistered {
				plan.ObservedRegisteredCount++
			} else {
				plan.ObservedUnregisteredCount++
				plan.LinksToCreate = append(plan.LinksToCreate, observation.ExpectedLink)
			}
		default:
			if observation.ExpectedLink != (ReceiptPurgeInverseLink{}) {
				return ReceiptPurgeLegacyBackfillPlan{}, ErrInvalidReceiptPurgeLegacyInventory
			}
			if plan.HeldStatus == "" {
				plan.HeldDocumentID = observation.DocumentID
				plan.HeldStatus = observation.Status
			}
		}
	}
	if plan.HeldStatus != "" {
		plan.LinksToCreate = nil
		plan.NextCursor = page.Request.Cursor
		plan.ObservedExhausted = false
	}
	return plan, nil
}

func ClassifyReceiptPurgeLegacyFindingProbe(
	tenantID string,
	firstDocumentID string,
) (ReceiptPurgeLegacyFindingProbeStatus, error) {
	if !telemetry.IsUUID(tenantID) ||
		(firstDocumentID != "" && !validFirestoreDocumentID(firstDocumentID)) {
		return "", ErrInvalidReceiptPurgeLegacyInventory
	}
	if firstDocumentID == "" {
		return ReceiptPurgeLegacyFindingEmptyObserved, nil
	}
	return ReceiptPurgeLegacyFindingUnsupported, nil
}

func optionalLegacyLookahead(page ReceiptPurgeLegacyInventoryPage) []string {
	if page.LookaheadDocumentID == "" {
		return nil
	}
	return []string{page.LookaheadDocumentID}
}

func cloneReceiptPurgeLegacyInventoryPage(
	page ReceiptPurgeLegacyInventoryPage,
) ReceiptPurgeLegacyInventoryPage {
	page.DocumentIDs = append([]string(nil), page.DocumentIDs...)
	return page
}

func validReceiptPurgeLegacyTargetStatus(status ReceiptPurgeLegacyTargetStatus) bool {
	switch status {
	case ReceiptPurgeLegacyTargetRegistered,
		ReceiptPurgeLegacyTargetUnregistered,
		ReceiptPurgeLegacyTargetMalformedChild,
		ReceiptPurgeLegacyTargetForeignChild,
		ReceiptPurgeLegacyTargetLinkageDrift,
		ReceiptPurgeLegacyTargetFencedUnregistered:
		return true
	default:
		return false
	}
}
