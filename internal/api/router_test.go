package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/wyw14/cry-103/internal/feed"
	"github.com/wyw14/cry-103/internal/incident"
	"github.com/wyw14/cry-103/internal/landing"
	"github.com/wyw14/cry-103/internal/otdr"
	"github.com/wyw14/cry-103/internal/platform"
	"github.com/wyw14/cry-103/internal/repair"
	"github.com/wyw14/cry-103/internal/repeater"
	"github.com/wyw14/cry-103/internal/route"
	"github.com/wyw14/cry-103/internal/span"
	"github.com/wyw14/cry-103/internal/store"
	"github.com/wyw14/cry-103/internal/switchover"
	"github.com/wyw14/cry-103/internal/telemetry"
	"github.com/wyw14/cry-103/internal/topology"
)

func testServer(t *testing.T) *Server {
	return serverWithClock(t, fixedClock{now: time.Date(2026, 8, 24, 3, 0, 0, 0, time.UTC)})
}

func serverWithClock(t *testing.T, clock platform.Clock) *Server {
	t.Helper()
	ids := platform.UUIDGenerator{}
	registry := topology.NewRegistry()
	if _, err := registry.Register("S3", topology.PathPrimary, clock.Now()); err != nil {
		t.Fatal(err)
	}
	target, err := span.New("S3", "S3", 126, clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	path, err := route.New("R-S3", "S3", clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	drainer := route.NewDrainer(0)
	activator := route.NewActivator(registry)
	receipts, err := landing.NewReceiptRegistry(clock.Now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	permitService := repair.NewPermitService(feed.NewIsolationService(), receipts, ids, clock.Now)
	circuit, err := feed.NewCircuit("B", "east", clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	permitService.RegisterCircuit(circuit)
	repeaterAsset, err := repeater.New("R07", clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	lifecycle := incident.NewRepeaterLifecycle()
	projector := repeater.NewHealthProjector(lifecycle, 85)
	projector.Register(repeaterAsset)
	repository := telemetry.NewRepository()
	acceptance := repair.NewAcceptanceService(switchover.NewRestorer(drainer, activator, registry, ids, clock.Now), clock.Now)
	acceptance.Register("S3", target, path)
	services := &Services{
		Clock: clock, IDs: ids, Journal: store.NewJournal(filepath.Join(t.TempDir(), "events.jsonl")),
		Spans: map[string]*span.Span{"S3": target}, Routes: map[string]*route.Route{"R-S3": path},
		Repeaters: map[string]*repeater.Repeater{"R07": repeaterAsset}, Topology: registry,
		OTDR:        otdr.NewSessionService(registry, landing.NewScanner(registry, 0), ids, clock.Now),
		Switchovers: switchover.NewSessionService(drainer, activator, ids, clock.Now, time.Second),
		Permits:     permitService, Receipts: receipts, Acceptance: acceptance,
		Telemetry: telemetry.NewIngestor(repository, clock.Now, projector), TelemetryRepository: repository,
		Admission: incident.NewAdmissionService(telemetry.NewMuteManager(clock.Now)),
	}
	return NewServer(services)
}

type fixedClock struct {
	now time.Time
}

func (c fixedClock) Now() time.Time {
	return c.now
}

type mutableClock struct {
	now time.Time
}

func (c *mutableClock) Now() time.Time {
	return c.now
}

func (c *mutableClock) Advance(d time.Duration) {
	c.now = c.now.Add(d)
}

func TestHealthAndScanHTTPFlow(t *testing.T) {
	server := testServer(t)
	health := httptest.NewRecorder()
	server.Handler().ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if health.Code != http.StatusOK {
		t.Fatalf("health status=%d body=%s", health.Code, health.Body.String())
	}

	body, _ := json.Marshal(ScanRequest{StationID: "east", LengthKM: 126, Chunks: 3})
	scan := httptest.NewRecorder()
	server.Handler().ServeHTTP(scan, httptest.NewRequest(http.MethodPost, "/api/spans/S3/otdr-scans", bytes.NewReader(body)))
	if scan.Code != http.StatusCreated {
		t.Fatalf("scan status=%d body=%s", scan.Code, scan.Body.String())
	}
	var trace otdr.Trace
	if err := json.Unmarshal(scan.Body.Bytes(), &trace); err != nil {
		t.Fatal(err)
	}
	if trace.SpanID != "S3" || len(trace.Chunks) != 3 {
		t.Fatalf("unexpected trace: %+v", trace)
	}

	status := httptest.NewRecorder()
	server.Handler().ServeHTTP(status, httptest.NewRequest(http.MethodGet, "/api/system/status", nil))
	if status.Code != http.StatusOK {
		t.Fatalf("status code=%d body=%s", status.Code, status.Body.String())
	}
}

func TestReceiptIdempotencyConflictAndExpiryFlow(t *testing.T) {
	clock := &mutableClock{now: time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)}
	server := serverWithClock(t, clock)

	isolationBody, _ := json.Marshal(IsolationRequest{CircuitID: "B"})
	isolation := httptest.NewRecorder()
	server.Handler().ServeHTTP(isolation, httptest.NewRequest(http.MethodPost, "/api/landings/east/feed-isolations", bytes.NewReader(isolationBody)))
	if isolation.Code != http.StatusAccepted {
		t.Fatalf("isolation status=%d body=%s", isolation.Code, isolation.Body.String())
	}
	var permit repair.Permit
	if err := json.Unmarshal(isolation.Body.Bytes(), &permit); err != nil {
		t.Fatal(err)
	}

	receiptBody, _ := json.Marshal(ReceiptRequest{CircuitID: "B", OperationID: permit.OperationID, Isolated: true})
	recorded := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorded, httptest.NewRequest(http.MethodPost, "/api/landings/east/feed-receipts", bytes.NewReader(receiptBody)))
	if recorded.Code != http.StatusCreated {
		t.Fatalf("receipt status=%d body=%s", recorded.Code, recorded.Body.String())
	}

	duplicate := httptest.NewRecorder()
	server.Handler().ServeHTTP(duplicate, httptest.NewRequest(http.MethodPost, "/api/landings/east/feed-receipts", bytes.NewReader(receiptBody)))
	if duplicate.Code != http.StatusOK {
		t.Fatalf("duplicate status=%d body=%s", duplicate.Code, duplicate.Body.String())
	}
	if !bytes.Equal(duplicate.Body.Bytes(), recorded.Body.Bytes()) {
		t.Fatalf("duplicate returned %s, want identical result %s", duplicate.Body.String(), recorded.Body.String())
	}

	conflictBody, _ := json.Marshal(ReceiptRequest{CircuitID: "B", OperationID: permit.OperationID, Isolated: false})
	conflict := httptest.NewRecorder()
	server.Handler().ServeHTTP(conflict, httptest.NewRequest(http.MethodPost, "/api/landings/east/feed-receipts", bytes.NewReader(conflictBody)))
	if conflict.Code != http.StatusConflict {
		t.Fatalf("conflict status=%d body=%s", conflict.Code, conflict.Body.String())
	}

	exceptions := httptest.NewRecorder()
	server.Handler().ServeHTTP(exceptions, httptest.NewRequest(http.MethodGet, "/api/landings/east/feed-receipt-exceptions", nil))
	if exceptions.Code != http.StatusOK {
		t.Fatalf("exceptions status=%d body=%s", exceptions.Code, exceptions.Body.String())
	}
	var listing struct {
		Items []landing.ReceiptException `json:"items"`
	}
	if err := json.Unmarshal(exceptions.Body.Bytes(), &listing); err != nil {
		t.Fatal(err)
	}
	if len(listing.Items) != 1 || !listing.Items[0].Existing.Isolated || listing.Items[0].Incoming.Isolated {
		t.Fatalf("unexpected exceptions: %+v", listing.Items)
	}

	evaluated := httptest.NewRecorder()
	server.Handler().ServeHTTP(evaluated, httptest.NewRequest(http.MethodPost, "/api/permits/"+permit.ID+"/evaluate", nil))
	if evaluated.Code != http.StatusOK {
		t.Fatalf("evaluate status=%d body=%s", evaluated.Code, evaluated.Body.String())
	}

	clock.Advance(2 * time.Minute)
	expired := httptest.NewRecorder()
	server.Handler().ServeHTTP(expired, httptest.NewRequest(http.MethodPost, "/api/permits/"+permit.ID+"/evaluate", nil))
	if expired.Code != http.StatusConflict {
		t.Fatalf("evaluate with expired receipt status=%d body=%s", expired.Code, expired.Body.String())
	}
}
