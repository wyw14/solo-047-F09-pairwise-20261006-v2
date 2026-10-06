package api

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/wyw14/cry-103/internal/landing"
)

var errReceiptConflict = errors.New("isolation receipt conflicts with the recorded fact; routed to exception reconciliation")

func (s *Server) startIsolation(writer http.ResponseWriter, request *http.Request) {
	stationID := chi.URLParam(request, "id")
	var input IsolationRequest
	if err := decodeJSON(request, &input); err != nil {
		writeError(writer, http.StatusBadRequest, err)
		return
	}
	permit, err := s.services.Permits.Request(stationID, input.CircuitID)
	if err != nil {
		writeError(writer, http.StatusConflict, err)
		return
	}
	if err := s.services.Persist("feed-isolation", permit.ID, permit); err != nil {
		writeError(writer, http.StatusInternalServerError, err)
		return
	}
	writeJSON(writer, http.StatusAccepted, permit)
}

func (s *Server) recordReceipt(writer http.ResponseWriter, request *http.Request) {
	stationID := chi.URLParam(request, "id")
	var input ReceiptRequest
	if err := decodeJSON(request, &input); err != nil {
		writeError(writer, http.StatusBadRequest, err)
		return
	}
	receipt := landing.IsolationReceipt{
		StationID: stationID, CircuitID: input.CircuitID, OperationID: input.OperationID,
		Isolated: input.Isolated, ReceivedAt: s.services.Clock.Now().UTC(),
	}
	stored, outcome := s.services.Receipts.Record(receipt)
	switch outcome {
	case landing.RecordDuplicate:
		writeJSON(writer, http.StatusOK, stored)
		return
	case landing.RecordConflict:
		exception := map[string]any{"existing": stored, "incoming": receipt}
		if err := s.services.Persist("feed-receipt-exception", input.OperationID, exception); err != nil {
			writeError(writer, http.StatusInternalServerError, err)
			return
		}
		writeError(writer, http.StatusConflict, errReceiptConflict)
		return
	}
	if err := s.services.Persist("feed-receipt", input.OperationID, stored); err != nil {
		writeError(writer, http.StatusInternalServerError, err)
		return
	}
	writeJSON(writer, http.StatusCreated, stored)
}

func (s *Server) listReceiptExceptions(writer http.ResponseWriter, request *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]any{"items": s.services.Receipts.Exceptions(chi.URLParam(request, "id"))})
}

func (s *Server) listReceipts(writer http.ResponseWriter, request *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]any{"items": s.services.Receipts.ForStation(chi.URLParam(request, "id"))})
}

func (s *Server) evaluatePermit(writer http.ResponseWriter, request *http.Request) {
	permit, err := s.services.Permits.Evaluate(chi.URLParam(request, "id"))
	if err != nil {
		writeError(writer, http.StatusConflict, err)
		return
	}
	if err := s.services.Persist("permit-groundable", permit.ID, permit); err != nil {
		writeError(writer, http.StatusInternalServerError, err)
		return
	}
	writeJSON(writer, http.StatusOK, permit)
}

func (s *Server) getPermit(writer http.ResponseWriter, request *http.Request) {
	permit, exists := s.services.Permits.Get(chi.URLParam(request, "id"))
	if !exists {
		writeJSON(writer, http.StatusNotFound, ErrorResponse{Error: "permit not found"})
		return
	}
	writeJSON(writer, http.StatusOK, permit)
}

func (s *Server) groundPermit(writer http.ResponseWriter, request *http.Request) {
	permit, err := s.services.Permits.Ground(chi.URLParam(request, "id"))
	if err != nil {
		writeError(writer, http.StatusConflict, err)
		return
	}
	writeJSON(writer, http.StatusOK, permit)
}
