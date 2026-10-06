package repair

import (
	"testing"
	"time"

	"github.com/wyw14/cry-103/internal/feed"
	"github.com/wyw14/cry-103/internal/landing"
	"github.com/wyw14/cry-103/internal/platform"
)

func TestEvaluateConsumesMatchingUnexpiredFact(t *testing.T) {
	now := time.Date(2026, 8, 24, 3, 0, 0, 0, time.UTC)
	current := now
	clock := func() time.Time { return current }
	receipts := landing.NewReceiptRegistry(clock, time.Minute)
	service := NewPermitService(feed.NewIsolationService(), receipts, platform.UUIDGenerator{}, clock)
	circuit, err := feed.NewCircuit("B", "east", now)
	if err != nil {
		t.Fatal(err)
	}
	service.RegisterCircuit(circuit)
	permit, err := service.Request("east", "B")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Evaluate(permit.ID); err == nil {
		t.Fatal("evaluation must fail without a matching receipt")
	}
	receipts.Record(landing.IsolationReceipt{
		StationID: "east", CircuitID: "B", OperationID: permit.OperationID, Isolated: true, ReceivedAt: current,
	})
	current = now.Add(2 * time.Minute)
	if _, err := service.Evaluate(permit.ID); err == nil {
		t.Fatal("evaluation must not consume an expired isolation fact")
	}
}

func TestEvaluateGroundsPermitWithUsableFact(t *testing.T) {
	now := time.Date(2026, 8, 24, 3, 0, 0, 0, time.UTC)
	current := now
	clock := func() time.Time { return current }
	receipts := landing.NewReceiptRegistry(clock, 5*time.Minute)
	service := NewPermitService(feed.NewIsolationService(), receipts, platform.UUIDGenerator{}, clock)
	circuit, err := feed.NewCircuit("B", "east", now)
	if err != nil {
		t.Fatal(err)
	}
	service.RegisterCircuit(circuit)
	permit, err := service.Request("east", "B")
	if err != nil {
		t.Fatal(err)
	}
	receipts.Record(landing.IsolationReceipt{
		StationID: "east", CircuitID: "B", OperationID: permit.OperationID, Isolated: true, ReceivedAt: current,
	})
	evaluated, err := service.Evaluate(permit.ID)
	if err != nil {
		t.Fatal(err)
	}
	if evaluated.State != PermitGroundable {
		t.Fatalf("unexpected permit state: %+v", evaluated)
	}
}
