package landing

import (
	"testing"
	"time"
)

func TestDuplicateReceiptReturnsSameResult(t *testing.T) {
	now := time.Date(2026, 8, 24, 3, 0, 0, 0, time.UTC)
	registry := NewReceiptRegistry(func() time.Time { return now }, 5*time.Minute)
	first := IsolationReceipt{StationID: "east", CircuitID: "B", OperationID: "op-1", Isolated: true, ReceivedAt: now}
	initial := registry.Record(first)
	if initial.Outcome != ReceiptRecorded {
		t.Fatalf("expected recorded outcome, got %s", initial.Outcome)
	}
	result := registry.Record(IsolationReceipt{
		StationID: "east", CircuitID: "B", OperationID: "op-1", Isolated: true, ReceivedAt: now.Add(time.Minute),
	})
	if result.Outcome != ReceiptDuplicate {
		t.Fatalf("expected duplicate outcome, got %s", result.Outcome)
	}
	if result.Receipt != initial.Receipt {
		t.Fatalf("duplicate must return the originally stored receipt, got %+v want %+v", result.Receipt, initial.Receipt)
	}
	if result.Discrepancy != nil {
		t.Fatalf("duplicate must not raise a discrepancy: %+v", result.Discrepancy)
	}
	if receipts := registry.ForStation("east"); len(receipts) != 1 {
		t.Fatalf("duplicate must not overwrite or add receipts, got %+v", receipts)
	}
	if discrepancies := registry.Discrepancies("east"); len(discrepancies) != 0 {
		t.Fatalf("duplicate must not enter reconciliation, got %+v", discrepancies)
	}
}

func TestConflictingReceiptEntersReconciliation(t *testing.T) {
	now := time.Date(2026, 8, 24, 3, 0, 0, 0, time.UTC)
	registry := NewReceiptRegistry(func() time.Time { return now }, 5*time.Minute)
	registry.Record(IsolationReceipt{StationID: "east", CircuitID: "B", OperationID: "op-1", Isolated: true, ReceivedAt: now})
	result := registry.Record(IsolationReceipt{
		StationID: "east", CircuitID: "B", OperationID: "op-1", Isolated: false, ReceivedAt: now.Add(time.Minute),
	})
	if result.Outcome != ReceiptConflict {
		t.Fatalf("expected conflict outcome, got %s", result.Outcome)
	}
	if !result.Receipt.Isolated {
		t.Fatalf("conflict must keep the recorded receipt, got %+v", result.Receipt)
	}
	if result.Discrepancy == nil {
		t.Fatal("conflict must produce a reconciliation discrepancy")
	}
	if !result.Discrepancy.Recorded.Isolated || result.Discrepancy.Incoming.Isolated {
		t.Fatalf("discrepancy must capture recorded and incoming receipts: %+v", result.Discrepancy)
	}
	receipt, state := registry.Inspect("east", "B", "op-1")
	if state != FactUsable || !receipt.Isolated {
		t.Fatalf("recorded fact must survive a conflict, got %s %+v", state, receipt)
	}
	discrepancies := registry.Discrepancies("east")
	if len(discrepancies) != 1 || discrepancies[0].OperationID != "op-1" {
		t.Fatalf("conflict must be listed for reconciliation, got %+v", discrepancies)
	}
}

func TestReceiptFactExpires(t *testing.T) {
	now := time.Date(2026, 8, 24, 3, 0, 0, 0, time.UTC)
	current := now
	registry := NewReceiptRegistry(func() time.Time { return current }, time.Minute)
	registry.Record(IsolationReceipt{StationID: "east", CircuitID: "B", OperationID: "op-1", Isolated: true, ReceivedAt: now})
	if _, state := registry.Inspect("east", "B", "op-1"); state != FactUsable {
		t.Fatalf("fresh receipt must be usable, got %s", state)
	}
	current = now.Add(2 * time.Minute)
	receipt, state := registry.Inspect("east", "B", "op-1")
	if state != FactExpired {
		t.Fatalf("receipt past its validity must be expired, got %s", state)
	}
	if !receipt.ExpiresAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("unexpected expiry: %+v", receipt)
	}
}

func TestReceiptFactStates(t *testing.T) {
	now := time.Date(2026, 8, 24, 3, 0, 0, 0, time.UTC)
	registry := NewReceiptRegistry(func() time.Time { return now }, 5*time.Minute)
	if _, state := registry.Inspect("east", "B", "op-1"); state != FactMissing {
		t.Fatalf("unknown operation must be missing, got %s", state)
	}
	registry.Record(IsolationReceipt{StationID: "east", CircuitID: "B", OperationID: "op-1", Isolated: false, ReceivedAt: now})
	if _, state := registry.Inspect("east", "B", "op-1"); state != FactNotIsolated {
		t.Fatalf("non-isolated receipt must not be a usable fact, got %s", state)
	}
}
