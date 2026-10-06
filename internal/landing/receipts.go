package landing

import (
	"errors"
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
	RecordAccepted  RecordOutcome = "accepted"
	RecordDuplicate RecordOutcome = "duplicate"
	RecordConflict  RecordOutcome = "conflict"
)

type ReceiptException struct {
	StationID   string           `json:"station_id"`
	CircuitID   string           `json:"circuit_id"`
	OperationID string           `json:"operation_id"`
	Existing    IsolationReceipt `json:"existing"`
	Incoming    IsolationReceipt `json:"incoming"`
	RecordedAt  time.Time        `json:"recorded_at"`
}

type ReceiptRegistry struct {
	mu         sync.RWMutex
	receipts   map[string]IsolationReceipt
	exceptions []ReceiptException
	now        func() time.Time
	ttl        time.Duration
}

func NewReceiptRegistry(now func() time.Time, ttl time.Duration) (*ReceiptRegistry, error) {
	if now == nil || ttl <= 0 {
		return nil, errors.New("clock and positive receipt TTL are required")
	}
	return &ReceiptRegistry{receipts: make(map[string]IsolationReceipt), now: now, ttl: ttl}, nil
}

func receiptKey(stationID, circuitID, operationID string) string {
	return stationID + "\x00" + circuitID + "\x00" + operationID
}

// Record registers a receipt idempotently: an identical re-delivery returns the
// stored receipt unchanged, while conflicting content for the same key is kept
// out of the facts and routed to exception reconciliation.
func (r *ReceiptRegistry) Record(receipt IsolationReceipt) (IsolationReceipt, RecordOutcome) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := receiptKey(receipt.StationID, receipt.CircuitID, receipt.OperationID)
	existing, exists := r.receipts[key]
	if !exists {
		receipt.ExpiresAt = receipt.ReceivedAt.UTC().Add(r.ttl)
		r.receipts[key] = receipt
		return receipt, RecordAccepted
	}
	if existing.Isolated == receipt.Isolated {
		return existing, RecordDuplicate
	}
	r.exceptions = append(r.exceptions, ReceiptException{
		StationID: existing.StationID, CircuitID: existing.CircuitID, OperationID: existing.OperationID,
		Existing: existing, Incoming: receipt, RecordedAt: r.now().UTC(),
	})
	return existing, RecordConflict
}

// Match reports whether an unexpired isolated receipt exists for the key, so
// permit evaluation only consumes matching, still-valid isolation facts.
func (r *ReceiptRegistry) Match(stationID, circuitID, operationID string) (IsolationReceipt, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	receipt, exists := r.receipts[receiptKey(stationID, circuitID, operationID)]
	if !exists || !receipt.Isolated || !r.now().UTC().Before(receipt.ExpiresAt) {
		return IsolationReceipt{}, false
	}
	return receipt, true
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

func (r *ReceiptRegistry) Exceptions(stationID string) []ReceiptException {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var result []ReceiptException
	for _, exception := range r.exceptions {
		if exception.StationID == stationID {
			result = append(result, exception)
		}
	}
	return result
}
