package landing

import (
	"sync"
	"time"
)

type IsolationReceipt struct {
	StationID   string    `json:"station_id"`
	CircuitID   string    `json:"circuit_id"`
	OperationID string    `json:"operation_id"`
	Isolated    bool      `json:"isolated"`
	ReceivedAt  time.Time `json:"received_at"`
	ExpiresAt   time.Time `json:"expires_at"`
}

type RecordOutcome string

const (
	ReceiptRecorded  RecordOutcome = "recorded"
	ReceiptDuplicate RecordOutcome = "duplicate"
	ReceiptConflict  RecordOutcome = "conflict"
)

type RecordResult struct {
	Receipt     IsolationReceipt    `json:"receipt"`
	Outcome     RecordOutcome       `json:"outcome"`
	Discrepancy *ReceiptDiscrepancy `json:"discrepancy,omitempty"`
}

type ReceiptDiscrepancy struct {
	StationID   string           `json:"station_id"`
	CircuitID   string           `json:"circuit_id"`
	OperationID string           `json:"operation_id"`
	Recorded    IsolationReceipt `json:"recorded"`
	Incoming    IsolationReceipt `json:"incoming"`
	LoggedAt    time.Time        `json:"logged_at"`
}

type FactState string

const (
	FactMissing     FactState = "missing"
	FactNotIsolated FactState = "not-isolated"
	FactExpired     FactState = "expired"
	FactUsable      FactState = "usable"
)

type ReceiptRegistry struct {
	mu            sync.RWMutex
	receipts      map[string]IsolationReceipt
	discrepancies []ReceiptDiscrepancy
	now           func() time.Time
	ttl           time.Duration
}

func NewReceiptRegistry(now func() time.Time, ttl time.Duration) *ReceiptRegistry {
	return &ReceiptRegistry{receipts: make(map[string]IsolationReceipt), now: now, ttl: ttl}
}

func receiptKey(stationID, circuitID, operationID string) string {
	return stationID + "\x00" + circuitID + "\x00" + operationID
}

func (r *ReceiptRegistry) Record(receipt IsolationReceipt) RecordResult {
	r.mu.Lock()
	defer r.mu.Unlock()
	if receipt.ReceivedAt.IsZero() {
		receipt.ReceivedAt = r.now().UTC()
	}
	receipt.ExpiresAt = receipt.ReceivedAt.Add(r.ttl).UTC()
	key := receiptKey(receipt.StationID, receipt.CircuitID, receipt.OperationID)
	existing, exists := r.receipts[key]
	if !exists {
		r.receipts[key] = receipt
		return RecordResult{Receipt: receipt, Outcome: ReceiptRecorded}
	}
	if existing.Isolated == receipt.Isolated {
		return RecordResult{Receipt: existing, Outcome: ReceiptDuplicate}
	}
	discrepancy := ReceiptDiscrepancy{
		StationID: receipt.StationID, CircuitID: receipt.CircuitID, OperationID: receipt.OperationID,
		Recorded: existing, Incoming: receipt, LoggedAt: r.now().UTC(),
	}
	r.discrepancies = append(r.discrepancies, discrepancy)
	return RecordResult{Receipt: existing, Outcome: ReceiptConflict, Discrepancy: &discrepancy}
}

func (r *ReceiptRegistry) Inspect(stationID, circuitID, operationID string) (IsolationReceipt, FactState) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	receipt, exists := r.receipts[receiptKey(stationID, circuitID, operationID)]
	if !exists {
		return IsolationReceipt{}, FactMissing
	}
	if !receipt.Isolated {
		return receipt, FactNotIsolated
	}
	if !r.now().Before(receipt.ExpiresAt) {
		return receipt, FactExpired
	}
	return receipt, FactUsable
}

func (r *ReceiptRegistry) ForStation(stationID string) []IsolationReceipt {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var result []IsolationReceipt
	for _, receipt := range r.receipts {
		if receipt.StationID == stationID {
			result = append(result, receipt)
		}
	}
	return result
}

func (r *ReceiptRegistry) Discrepancies(stationID string) []ReceiptDiscrepancy {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var result []ReceiptDiscrepancy
	for _, discrepancy := range r.discrepancies {
		if discrepancy.StationID == stationID {
			result = append(result, discrepancy)
		}
	}
	return result
}
