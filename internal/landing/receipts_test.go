package landing

import (
	"testing"
	"time"
)

func TestNewReceiptRegistryValidatesClockAndTTL(t *testing.T) {
	if _, err := NewReceiptRegistry(nil, time.Minute); err == nil {
		t.Fatal("expected error for missing clock")
	}
	if _, err := NewReceiptRegistry(time.Now, 0); err == nil {
		t.Fatal("expected error for non-positive TTL")
	}
}

func TestRecordReturnsStoredReceiptForIdenticalDuplicate(t *testing.T) {
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	registry, err := NewReceiptRegistry(func() time.Time { return now }, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	first := IsolationReceipt{StationID: "east", CircuitID: "A", OperationID: "op-1", Isolated: true, ReceivedAt: now}
	stored, outcome := registry.Record(first)
	if outcome != RecordAccepted {
		t.Fatalf("first record outcome=%s", outcome)
	}
	if !stored.ExpiresAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("expires_at=%s, want %s", stored.ExpiresAt, now.Add(time.Minute))
	}
	duplicate := IsolationReceipt{StationID: "east", CircuitID: "A", OperationID: "op-1", Isolated: true, ReceivedAt: now.Add(10 * time.Second)}
	again, outcome := registry.Record(duplicate)
	if outcome != RecordDuplicate {
		t.Fatalf("duplicate outcome=%s", outcome)
	}
	if again != stored {
		t.Fatalf("duplicate returned %+v, want stored %+v", again, stored)
	}
	if got := registry.ForStation("east"); len(got) != 1 {
		t.Fatalf("expected one stored receipt, got %+v", got)
	}
}

func TestRecordRoutesConflictToExceptionReconciliation(t *testing.T) {
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	registry, err := NewReceiptRegistry(func() time.Time { return now }, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	stored, _ := registry.Record(IsolationReceipt{StationID: "east", CircuitID: "A", OperationID: "op-1", Isolated: true, ReceivedAt: now})
	conflicting := IsolationReceipt{StationID: "east", CircuitID: "A", OperationID: "op-1", Isolated: false, ReceivedAt: now.Add(5 * time.Second)}
	returned, outcome := registry.Record(conflicting)
	if outcome != RecordConflict {
		t.Fatalf("conflict outcome=%s", outcome)
	}
	if returned != stored {
		t.Fatalf("conflict returned %+v, want stored %+v", returned, stored)
	}
	exceptions := registry.Exceptions("east")
	if len(exceptions) != 1 {
		t.Fatalf("expected one exception, got %+v", exceptions)
	}
	exception := exceptions[0]
	if !exception.Existing.Isolated || exception.Incoming.Isolated {
		t.Fatalf("exception does not capture both versions: %+v", exception)
	}
	if !exception.RecordedAt.Equal(now) {
		t.Fatalf("exception recorded_at=%s, want %s", exception.RecordedAt, now)
	}
	if matched, ok := registry.Match("east", "A", "op-1"); !ok || !matched.Isolated {
		t.Fatalf("conflict must not overwrite the isolation fact: %+v %v", matched, ok)
	}
}

func TestMatchRequiresUnexpiredIsolatedFact(t *testing.T) {
	current := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	registry, err := NewReceiptRegistry(func() time.Time { return current }, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	registry.Record(IsolationReceipt{StationID: "east", CircuitID: "A", OperationID: "op-1", Isolated: true, ReceivedAt: current})
	if _, ok := registry.Match("east", "A", "op-1"); !ok {
		t.Fatal("fresh isolated receipt should match")
	}
	current = current.Add(time.Minute)
	if _, ok := registry.Match("east", "A", "op-1"); ok {
		t.Fatal("receipt must not match at its expiry boundary")
	}
	current = current.Add(time.Minute)
	if _, ok := registry.Match("east", "A", "op-1"); ok {
		t.Fatal("expired receipt must not match")
	}
}
