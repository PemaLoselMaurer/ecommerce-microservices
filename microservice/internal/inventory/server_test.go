package inventory

import (
	"context"
	"fmt"
	"sync"
	"testing"

	pb "ecommerce-microservices/proto"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// These tests exercise InventoryServiceServer directly, in
// process. Simulated flakiness is switched off by default so
// each test measures the behaviour it is actually about; the
// retry-facing behaviour has its own test below.

func newTestServer() *Server {
	return NewServer(InitialStock(), false)
}

func assertCode(t *testing.T, err error, want codes.Code) {
	t.Helper()

	if err == nil {
		t.Fatalf("expected error with code %s, got nil", want)
	}

	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected a gRPC status error, got %T: %v", err, err)
	}
	if st.Code() != want {
		t.Fatalf("expected code %s, got %s (%q)", want, st.Code(), st.Message())
	}
}

// ---------- GetStock ----------

func TestGetStockReturnsTheLevelForEachProduct(t *testing.T) {
	server := newTestServer()

	cases := []struct {
		productID string
		want      int32
	}{
		{"P001", 10},
		{"P002", 25},
		{"P003", 30},
		{"P004", 8},
		{"P005", 14},
		{"P006", 6},
		{"P007", 3},
		{"P008", 0},
	}

	for _, testCase := range cases {
		t.Run(testCase.productID, func(t *testing.T) {
			item, err := server.GetStock(context.Background(),
				&pb.GetStockRequest{ProductId: testCase.productID})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if item.GetProductId() != testCase.productID {
				t.Errorf("product ID: got %q, want %q", item.GetProductId(), testCase.productID)
			}
			if item.GetQuantity() != testCase.want {
				t.Errorf("quantity: got %d, want %d", item.GetQuantity(), testCase.want)
			}
		})
	}
}

func TestGetStockDistinguishesZeroStockFromNoRecord(t *testing.T) {
	server := newTestServer()

	// P008 is stocked at zero. That is a real record and must
	// succeed, or the storefront would show "unknown" instead
	// of "out of stock".
	item, err := server.GetStock(context.Background(), &pb.GetStockRequest{ProductId: "P008"})
	if err != nil {
		t.Fatalf("P008 is stocked at zero and should not error: %v", err)
	}
	if item.GetQuantity() != 0 {
		t.Errorf("quantity: got %d, want 0", item.GetQuantity())
	}

	// A product with no record at all is a different case.
	_, err = server.GetStock(context.Background(), &pb.GetStockRequest{ProductId: "P999"})
	assertCode(t, err, codes.NotFound)
}

func TestGetStockRejectsUnknownAndEmptyID(t *testing.T) {
	server := newTestServer()

	for _, productID := range []string{"P999", "", "p001", "unknown"} {
		t.Run("id="+productID, func(t *testing.T) {
			_, err := server.GetStock(context.Background(), &pb.GetStockRequest{ProductId: productID})
			assertCode(t, err, codes.NotFound)
		})
	}
}

// ---------- ReserveStock ----------

func TestReserveStockDecrementsTheLevel(t *testing.T) {
	server := newTestServer()

	item, err := server.ReserveStock(context.Background(),
		&pb.ReserveStockRequest{ProductId: "P001", Quantity: 3})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The reply carries the level after the reservation.
	if item.GetQuantity() != 7 {
		t.Errorf("returned quantity: got %d, want 7", item.GetQuantity())
	}

	// And the change must have been stored, not just reported.
	stored, err := server.GetStock(context.Background(), &pb.GetStockRequest{ProductId: "P001"})
	if err != nil {
		t.Fatalf("unexpected error reading stock back: %v", err)
	}
	if stored.GetQuantity() != 7 {
		t.Errorf("stored quantity: got %d, want 7", stored.GetQuantity())
	}
}

func TestReserveStockAllowsTakingTheLastUnits(t *testing.T) {
	server := newTestServer()

	// P007 holds exactly 3. Reserving all 3 must succeed;
	// an off-by-one here would block legitimate final sales.
	item, err := server.ReserveStock(context.Background(),
		&pb.ReserveStockRequest{ProductId: "P007", Quantity: 3})
	if err != nil {
		t.Fatalf("reserving the last units failed: %v", err)
	}
	if item.GetQuantity() != 0 {
		t.Errorf("quantity: got %d, want 0", item.GetQuantity())
	}
}

func TestReserveStockRejectsMoreThanIsAvailable(t *testing.T) {
	cases := []struct {
		name      string
		productID string
		quantity  int32
	}{
		{"one more than stocked", "P004", 9},
		{"far more than stocked", "P001", 1000},
		{"anything from an empty shelf", "P008", 1},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			server := newTestServer()

			_, err := server.ReserveStock(context.Background(),
				&pb.ReserveStockRequest{ProductId: testCase.productID, Quantity: testCase.quantity})

			assertCode(t, err, codes.ResourceExhausted)
		})
	}
}

func TestReserveStockLeavesTheLevelUntouchedWhenItFails(t *testing.T) {
	server := newTestServer()

	before, _ := server.GetStock(context.Background(), &pb.GetStockRequest{ProductId: "P004"})

	_, err := server.ReserveStock(context.Background(),
		&pb.ReserveStockRequest{ProductId: "P004", Quantity: 99})
	if err == nil {
		t.Fatal("expected the over-large reservation to fail")
	}

	after, _ := server.GetStock(context.Background(), &pb.GetStockRequest{ProductId: "P004"})

	if before.GetQuantity() != after.GetQuantity() {
		t.Errorf("a failed reservation changed the stock level: %d -> %d",
			before.GetQuantity(), after.GetQuantity())
	}
}

func TestReserveStockRejectsInvalidQuantity(t *testing.T) {
	server := newTestServer()

	for _, quantity := range []int32{0, -1, -100} {
		t.Run(fmt.Sprintf("quantity=%d", quantity), func(t *testing.T) {
			_, err := server.ReserveStock(context.Background(),
				&pb.ReserveStockRequest{ProductId: "P001", Quantity: quantity})

			assertCode(t, err, codes.InvalidArgument)
		})
	}
}

func TestReserveStockRejectsUnknownProduct(t *testing.T) {
	server := newTestServer()

	_, err := server.ReserveStock(context.Background(),
		&pb.ReserveStockRequest{ProductId: "P999", Quantity: 1})

	assertCode(t, err, codes.NotFound)
}

func TestReserveStockChecksQuantityBeforeProductExistence(t *testing.T) {
	server := newTestServer()

	// A request that is wrong in two ways should be reported as
	// malformed, since the caller has to fix that first.
	_, err := server.ReserveStock(context.Background(),
		&pb.ReserveStockRequest{ProductId: "P999", Quantity: 0})

	assertCode(t, err, codes.InvalidArgument)
}

func TestConcurrentReservationsNeverOversell(t *testing.T) {
	server := newTestServer()

	// P001 holds 10. Twenty callers each try to take 1, so
	// exactly 10 must succeed and the shelf must end at zero.
	const callers = 20

	var wait sync.WaitGroup
	results := make(chan error, callers)

	for i := 0; i < callers; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, err := server.ReserveStock(context.Background(),
				&pb.ReserveStockRequest{ProductId: "P001", Quantity: 1})
			results <- err
		}()
	}

	wait.Wait()
	close(results)

	succeeded := 0
	for err := range results {
		if err == nil {
			succeeded++
		}
	}

	if succeeded != 10 {
		t.Errorf("%d reservations succeeded, want exactly 10", succeeded)
	}

	remaining, _ := server.GetStock(context.Background(), &pb.GetStockRequest{ProductId: "P001"})
	if remaining.GetQuantity() != 0 {
		t.Errorf("remaining stock: got %d, want 0", remaining.GetQuantity())
	}
}

// ---------- simulated transient failures ----------

func TestFlakyReserveFailsTwoCallsInThree(t *testing.T) {
	server := newTestServer()
	server.flaky = true

	// The simulation exists to give the Retry pattern something
	// to recover from: the first two attempts fail with
	// Unavailable — a code the retry interceptor treats as
	// worth trying again — and the third succeeds.
	_, first := server.ReserveStock(context.Background(),
		&pb.ReserveStockRequest{ProductId: "P001", Quantity: 1})
	assertCode(t, first, codes.Unavailable)

	_, second := server.ReserveStock(context.Background(),
		&pb.ReserveStockRequest{ProductId: "P001", Quantity: 1})
	assertCode(t, second, codes.Unavailable)

	_, third := server.ReserveStock(context.Background(),
		&pb.ReserveStockRequest{ProductId: "P001", Quantity: 1})
	if third != nil {
		t.Fatalf("the third attempt should succeed, got: %v", third)
	}
}

func TestFlakyFailuresDoNotConsumeStock(t *testing.T) {
	server := newTestServer()
	server.flaky = true

	before, _ := server.GetStock(context.Background(), &pb.GetStockRequest{ProductId: "P001"})

	// The two simulated failures happen before any state is
	// touched, which is what makes retrying safe.
	_, _ = server.ReserveStock(context.Background(), &pb.ReserveStockRequest{ProductId: "P001", Quantity: 1})
	_, _ = server.ReserveStock(context.Background(), &pb.ReserveStockRequest{ProductId: "P001", Quantity: 1})

	after, _ := server.GetStock(context.Background(), &pb.GetStockRequest{ProductId: "P001"})

	if before.GetQuantity() != after.GetQuantity() {
		t.Errorf("transient failures consumed stock: %d -> %d",
			before.GetQuantity(), after.GetQuantity())
	}
}

func TestFlakinessIsOffByDefault(t *testing.T) {
	server := newTestServer()

	// With the simulation disabled every call must succeed
	// first time, so tests and demos are repeatable.
	for i := 0; i < 5; i++ {
		if _, err := server.ReserveStock(context.Background(),
			&pb.ReserveStockRequest{ProductId: "P003", Quantity: 1}); err != nil {
			t.Fatalf("attempt %d failed unexpectedly: %v", i+1, err)
		}
	}
}

// ---------- stock adjustments and alerts ----------

func TestApplyAdjustmentRaisesAndLowersStock(t *testing.T) {
	server := newTestServer()

	alert, err := server.applyAdjustment(&pb.StockAdjustment{ProductId: "P001", Delta: 5})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if alert.GetNewQuantity() != 15 {
		t.Errorf("after +5: got %d, want 15", alert.GetNewQuantity())
	}
	if alert.GetLowStock() {
		t.Error("15 units should not be flagged as low stock")
	}

	alert, err = server.applyAdjustment(&pb.StockAdjustment{ProductId: "P001", Delta: -12})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if alert.GetNewQuantity() != 3 {
		t.Errorf("after -12: got %d, want 3", alert.GetNewQuantity())
	}
	if !alert.GetLowStock() {
		t.Error("3 units is below the threshold and should be flagged as low stock")
	}
}

func TestApplyAdjustmentClampsAtZero(t *testing.T) {
	server := newTestServer()

	// Removing more than is present must floor at zero rather
	// than leave a negative shelf.
	alert, err := server.applyAdjustment(&pb.StockAdjustment{ProductId: "P004", Delta: -100})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if alert.GetNewQuantity() != 0 {
		t.Errorf("quantity: got %d, want 0", alert.GetNewQuantity())
	}
}

func TestApplyAdjustmentRejectsUnknownProduct(t *testing.T) {
	server := newTestServer()

	_, err := server.applyAdjustment(&pb.StockAdjustment{ProductId: "P999", Delta: 1})

	assertCode(t, err, codes.NotFound)
}

func TestLowStockThresholdBoundary(t *testing.T) {
	cases := []struct {
		name        string
		target      int32
		wantLowFlag bool
	}{
		{"below the threshold", 4, true},
		{"exactly at the threshold", LowStockThreshold, false},
		{"above the threshold", 6, false},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			server := newTestServer()

			// P001 starts at 10; adjust it to the level under test.
			alert, err := server.applyAdjustment(&pb.StockAdjustment{
				ProductId: "P001",
				Delta:     testCase.target - 10,
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if alert.GetNewQuantity() != testCase.target {
				t.Fatalf("quantity: got %d, want %d", alert.GetNewQuantity(), testCase.target)
			}
			if alert.GetLowStock() != testCase.wantLowFlag {
				t.Errorf("low stock flag at %d: got %t, want %t",
					testCase.target, alert.GetLowStock(), testCase.wantLowFlag)
			}
		})
	}
}
