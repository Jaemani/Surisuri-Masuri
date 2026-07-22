package ingest

import (
	"errors"
	"testing"
	"time"
)

func TestBuildReceiptPurgeLegacyInventoryPageBoundsLookahead(t *testing.T) {
	request := receiptPurgeLegacyInventoryRequestFixture(2)
	ids := []string{
		"019c5af2-7a6b-7d1a-850c-02dc21343a25",
		"019c5af2-7a6b-7d1a-850c-02dc21343a26",
		"019c5af2-7a6b-7d1a-850c-02dc21343a27",
	}
	page, err := BuildReceiptPurgeLegacyInventoryPage(request, ids)
	if err != nil {
		t.Fatalf("BuildReceiptPurgeLegacyInventoryPage() = %v", err)
	}
	if len(page.DocumentIDs) != 2 || page.DocumentIDs[0] != ids[0] ||
		page.DocumentIDs[1] != ids[1] || page.LookaheadDocumentID != ids[2] ||
		page.NextCursor != ids[1] || page.ObservedExhausted {
		t.Fatalf("page = %#v", page)
	}
	ids[0] = "mutated"
	if page.DocumentIDs[0] == ids[0] {
		t.Fatal("page retained caller-owned identities")
	}
}

func TestBuildReceiptPurgeLegacyInventoryPageRejectsInvalidInput(t *testing.T) {
	valid := receiptPurgeLegacyInventoryRequestFixture(2)
	first := "019c5af2-7a6b-7d1a-850c-02dc21343a25"
	second := "019c5af2-7a6b-7d1a-850c-02dc21343a26"
	for _, test := range []struct {
		name    string
		request ReceiptPurgeLegacyInventoryRequest
		ids     []string
	}{
		{name: "tenant", request: ReceiptPurgeLegacyInventoryRequest{PageSize: 1}},
		{name: "page size zero", request: ReceiptPurgeLegacyInventoryRequest{TenantID: valid.TenantID}},
		{name: "page size high", request: ReceiptPurgeLegacyInventoryRequest{TenantID: valid.TenantID, PageSize: 26}},
		{name: "cursor", request: ReceiptPurgeLegacyInventoryRequest{TenantID: valid.TenantID, Cursor: "legacy", PageSize: 1}},
		{name: "too many", request: valid, ids: []string{first, second, second + "a", second + "b"}},
		{name: "duplicate", request: valid, ids: []string{first, first}},
		{name: "descending", request: valid, ids: []string{second, first}},
		{name: "unsafe id", request: valid, ids: []string{"nested/document"}},
		{name: "at cursor", request: ReceiptPurgeLegacyInventoryRequest{TenantID: valid.TenantID, Cursor: first, PageSize: 1}, ids: []string{first}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := BuildReceiptPurgeLegacyInventoryPage(test.request, test.ids); !errors.Is(err, ErrInvalidReceiptPurgeLegacyInventory) {
				t.Fatalf("BuildReceiptPurgeLegacyInventoryPage() = %v", err)
			}
		})
	}
}

func TestPlanReceiptPurgeLegacyBackfillCreatesOnlyMissingLinks(t *testing.T) {
	request := receiptPurgeLegacyInventoryRequestFixture(2)
	ids := []string{
		"019c5af2-7a6b-7d1a-850c-02dc21343a25",
		"019c5af2-7a6b-7d1a-850c-02dc21343a26",
	}
	page, err := BuildReceiptPurgeLegacyInventoryPage(request, ids)
	if err != nil {
		t.Fatalf("BuildReceiptPurgeLegacyInventoryPage() = %v", err)
	}
	registered := receiptPurgeLegacyLinkFixture(t, request.TenantID, ids[0])
	unregistered := receiptPurgeLegacyLinkFixture(t, request.TenantID, ids[1])
	plan, err := PlanReceiptPurgeLegacyBackfill(page, []ReceiptPurgeLegacyTargetObservation{
		{DocumentID: ids[0], Status: ReceiptPurgeLegacyTargetRegistered, ExpectedLink: registered},
		{DocumentID: ids[1], Status: ReceiptPurgeLegacyTargetUnregistered, ExpectedLink: unregistered},
	})
	if err != nil {
		t.Fatalf("PlanReceiptPurgeLegacyBackfill() = %v", err)
	}
	if plan.ObservedRegisteredCount != 1 || plan.ObservedUnregisteredCount != 1 ||
		len(plan.LinksToCreate) != 1 || plan.LinksToCreate[0] != unregistered ||
		plan.HeldStatus != "" || plan.NextCursor != ids[1] || !plan.ObservedExhausted {
		t.Fatalf("plan = %#v", plan)
	}
}

func TestPlanReceiptPurgeLegacyBackfillPoisonHoldsWholePage(t *testing.T) {
	for _, poison := range []ReceiptPurgeLegacyTargetStatus{
		ReceiptPurgeLegacyTargetMalformedChild,
		ReceiptPurgeLegacyTargetForeignChild,
		ReceiptPurgeLegacyTargetLinkageDrift,
		ReceiptPurgeLegacyTargetFencedUnregistered,
	} {
		t.Run(string(poison), func(t *testing.T) {
			request := receiptPurgeLegacyInventoryRequestFixture(2)
			ids := []string{
				"019c5af2-7a6b-7d1a-850c-02dc21343a25",
				"019c5af2-7a6b-7d1a-850c-02dc21343a26",
			}
			page, _ := BuildReceiptPurgeLegacyInventoryPage(request, ids)
			missing := receiptPurgeLegacyLinkFixture(t, request.TenantID, ids[0])
			plan, err := PlanReceiptPurgeLegacyBackfill(page, []ReceiptPurgeLegacyTargetObservation{
				{DocumentID: ids[0], Status: ReceiptPurgeLegacyTargetUnregistered, ExpectedLink: missing},
				{DocumentID: ids[1], Status: poison},
			})
			if err != nil {
				t.Fatalf("PlanReceiptPurgeLegacyBackfill() = %v", err)
			}
			if len(plan.LinksToCreate) != 0 || plan.HeldDocumentID != ids[1] ||
				plan.HeldStatus != poison || plan.NextCursor != request.Cursor || plan.ObservedExhausted ||
				plan.ObservedUnregisteredCount != 1 {
				t.Fatalf("held plan = %#v", plan)
			}
		})
	}
}

func TestBuildReceiptPurgeLegacyInventoryPageEmptyObservationKeepsCursor(t *testing.T) {
	request := receiptPurgeLegacyInventoryRequestFixture(2)
	request.Cursor = "019c5af2-7a6b-7d1a-850c-02dc21343a25"
	page, err := BuildReceiptPurgeLegacyInventoryPage(request, nil)
	if err != nil {
		t.Fatalf("BuildReceiptPurgeLegacyInventoryPage() = %v", err)
	}
	if len(page.DocumentIDs) != 0 || page.LookaheadDocumentID != "" ||
		page.NextCursor != request.Cursor || !page.ObservedExhausted {
		t.Fatalf("empty page = %#v", page)
	}
}

func TestPlanReceiptPurgeLegacyBackfillRejectsTamperedPage(t *testing.T) {
	request := receiptPurgeLegacyInventoryRequestFixture(1)
	id := "019c5af2-7a6b-7d1a-850c-02dc21343a25"
	page, _ := BuildReceiptPurgeLegacyInventoryPage(request, []string{id})
	link := receiptPurgeLegacyLinkFixture(t, request.TenantID, id)
	observation := []ReceiptPurgeLegacyTargetObservation{{
		DocumentID: id, Status: ReceiptPurgeLegacyTargetRegistered, ExpectedLink: link,
	}}
	for _, mutate := range []func(*ReceiptPurgeLegacyInventoryPage){
		func(value *ReceiptPurgeLegacyInventoryPage) { value.NextCursor = request.Cursor },
		func(value *ReceiptPurgeLegacyInventoryPage) { value.ObservedExhausted = false },
		func(value *ReceiptPurgeLegacyInventoryPage) { value.LookaheadDocumentID = id },
	} {
		tampered := page
		mutate(&tampered)
		if _, err := PlanReceiptPurgeLegacyBackfill(tampered, observation); !errors.Is(err, ErrInvalidReceiptPurgeLegacyInventory) {
			t.Fatalf("PlanReceiptPurgeLegacyBackfill(%#v) = %v", tampered, err)
		}
	}
}

func TestPlanReceiptPurgeLegacyBackfillMalformedSafeIDHoldsCursor(t *testing.T) {
	request := receiptPurgeLegacyInventoryRequestFixture(1)
	page, err := BuildReceiptPurgeLegacyInventoryPage(request, []string{"legacy-cleanup-id"})
	if err != nil {
		t.Fatalf("BuildReceiptPurgeLegacyInventoryPage() = %v", err)
	}
	plan, err := PlanReceiptPurgeLegacyBackfill(page, []ReceiptPurgeLegacyTargetObservation{{
		DocumentID: "legacy-cleanup-id", Status: ReceiptPurgeLegacyTargetMalformedChild,
	}})
	if err != nil {
		t.Fatalf("PlanReceiptPurgeLegacyBackfill() = %v", err)
	}
	if plan.HeldDocumentID != "legacy-cleanup-id" ||
		plan.HeldStatus != ReceiptPurgeLegacyTargetMalformedChild ||
		plan.NextCursor != request.Cursor || plan.ObservedExhausted {
		t.Fatalf("held plan = %#v", plan)
	}
}

func TestPlanReceiptPurgeLegacyBackfillRejectsObservationDrift(t *testing.T) {
	request := receiptPurgeLegacyInventoryRequestFixture(1)
	id := "019c5af2-7a6b-7d1a-850c-02dc21343a25"
	page, _ := BuildReceiptPurgeLegacyInventoryPage(request, []string{id})
	validLink := receiptPurgeLegacyLinkFixture(t, request.TenantID, id)
	for _, observations := range [][]ReceiptPurgeLegacyTargetObservation{
		nil,
		{{DocumentID: "foreign", Status: ReceiptPurgeLegacyTargetUnregistered, ExpectedLink: validLink}},
		{{DocumentID: id, Status: "future"}},
		{{DocumentID: id, Status: ReceiptPurgeLegacyTargetUnregistered}},
		{{DocumentID: id, Status: ReceiptPurgeLegacyTargetMalformedChild, ExpectedLink: validLink}},
	} {
		if _, err := PlanReceiptPurgeLegacyBackfill(page, observations); !errors.Is(err, ErrInvalidReceiptPurgeLegacyInventory) {
			t.Fatalf("PlanReceiptPurgeLegacyBackfill(%#v) = %v", observations, err)
		}
	}
}

func TestClassifyReceiptPurgeLegacyFindingProbeIsScoped(t *testing.T) {
	tenantID := receiptPurgeLegacyInventoryRequestFixture(1).TenantID
	status, err := ClassifyReceiptPurgeLegacyFindingProbe(tenantID, "")
	if err != nil || status != ReceiptPurgeLegacyFindingEmptyObserved {
		t.Fatalf("empty probe = %q, %v", status, err)
	}
	status, err = ClassifyReceiptPurgeLegacyFindingProbe(tenantID, "finding-legacy-001")
	if err != nil || status != ReceiptPurgeLegacyFindingUnsupported {
		t.Fatalf("nonempty probe = %q, %v", status, err)
	}
	if _, err := ClassifyReceiptPurgeLegacyFindingProbe(tenantID, "nested/document"); !errors.Is(err, ErrInvalidReceiptPurgeLegacyInventory) {
		t.Fatalf("unsafe probe = %v", err)
	}
}

func receiptPurgeLegacyInventoryRequestFixture(pageSize int) ReceiptPurgeLegacyInventoryRequest {
	return ReceiptPurgeLegacyInventoryRequest{
		TenantID: "019c5af2-7a6b-7d1a-850c-02dc21343a23",
		PageSize: pageSize,
	}
}

func receiptPurgeLegacyLinkFixture(
	t *testing.T,
	tenantID string,
	documentID string,
) ReceiptPurgeInverseLink {
	t.Helper()
	link, err := BuildReceiptPurgeInverseLink(ReceiptPurgeLinkedChildIdentity{
		TenantID: tenantID, ReceiptID: "019c5af2-7a6b-7d1a-850c-02dc21343a24",
		Kind: ReceiptPurgeLinkCleanupTarget, DocumentID: documentID,
		CreatedAt: time.Date(2026, time.July, 23, 9, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("BuildReceiptPurgeInverseLink() = %v", err)
	}
	return link
}
