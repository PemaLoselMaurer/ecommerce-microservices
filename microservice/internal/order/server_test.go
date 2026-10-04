package order

import (
	"context"
	"strings"
	"testing"

	pb "ecommerce-microservices/proto"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// These tests exercise the Order Service with test doubles in
// place of its five dependencies. Nothing listens on a port
// and no other service runs, so they are unit tests: they
// measure the Order Service's own orchestration and error
// handling, not the network between services. The wiring over
// real gRPC is covered by the integration tests in Part C.

// testKit bundles a server with the doubles behind it, so a
// test can assert on what each dependency saw.
type testKit struct {
	server       *Server
	customer     *fakeCustomerClient
	product      *fakeProductClient
	inventory    *fakeInventoryClient
	payment      *fakePaymentClient
	notification *fakeNotificationClient
	pricing      *fakePricingClient
}

// newTestKit builds an Order Service whose dependencies all
// behave. Individual tests then break the one they are about.
func newTestKit() *testKit {
	customer := &fakeCustomerClient{customers: map[string]*pb.Customer{
		"C001": {CustomerId: "C001", Name: "Alice Nguyen", Email: "alice@example.com"},
		"C002": {CustomerId: "C002", Name: "Bob Tan", Email: "bob@example.com"},
	}}

	product := &fakeProductClient{products: map[string]*pb.Product{
		"P001": {ProductId: "P001", Name: "Laptop", Price: 75000},
		"P002": {ProductId: "P002", Name: "Mechanical Keyboard", Price: 4500, Category: "accessories"},
		"P003": {ProductId: "P003", Name: "Wireless Mouse", Price: 1800},
	}}

	inventory := &fakeInventoryClient{stock: map[string]int32{
		"P001": 10,
		"P002": 25,
		"P003": 2,
	}}

	payment := &fakePaymentClient{}
	notification := &fakeNotificationClient{}
	pricing := &fakePricingClient{}

	return &testKit{
		server: NewServer(Clients{
			Customer:     customer,
			Product:      product,
			Inventory:    inventory,
			Payment:      payment,
			Notification: notification,
			Pricing:      pricing,
		}),
		customer:     customer,
		product:      product,
		inventory:    inventory,
		payment:      payment,
		notification: notification,
		pricing:      pricing,
	}
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

// ---------- the success path ----------

func TestCreateOrderPlacesAnOrder(t *testing.T) {
	kit := newTestKit()

	order, err := kit.server.CreateOrder(context.Background(), &pb.CreateOrderRequest{
		CustomerId: "C001",
		ProductId:  "P002",
		Quantity:   2,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if order.GetOrderId() != "ORD0001" {
		t.Errorf("order ID: got %q, want %q", order.GetOrderId(), "ORD0001")
	}
	if order.GetCustomerId() != "C001" {
		t.Errorf("customer ID: got %q, want %q", order.GetCustomerId(), "C001")
	}
	if order.GetProductId() != "P002" {
		t.Errorf("product ID: got %q, want %q", order.GetProductId(), "P002")
	}
	if order.GetProductName() != "Mechanical Keyboard" {
		t.Errorf("product name: got %q, want %q", order.GetProductName(), "Mechanical Keyboard")
	}
	if order.GetQuantity() != 2 {
		t.Errorf("quantity: got %d, want 2", order.GetQuantity())
	}
	// 4500 × 2 — the total is computed here, not supplied by
	// the caller, so a client cannot choose its own price.
	if order.GetTotalPrice() != 9000 {
		t.Errorf("total price: got %v, want 9000", order.GetTotalPrice())
	}
	if order.GetStatus() != "CONFIRMED" {
		t.Errorf("status: got %q, want CONFIRMED", order.GetStatus())
	}
	if order.GetPaymentId() != "PAY0001" {
		t.Errorf("payment ID: got %q, want %q", order.GetPaymentId(), "PAY0001")
	}
}

func TestCreateOrderCallsEveryDependencyOnce(t *testing.T) {
	kit := newTestKit()

	if _, err := kit.server.CreateOrder(context.Background(), &pb.CreateOrderRequest{
		CustomerId: "C001", ProductId: "P002", Quantity: 1,
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for name, calls := range map[string]int{
		"customer":     kit.customer.calls,
		"product":      kit.product.calls,
		"inventory":    kit.inventory.calls,
		"payment":      kit.payment.calls,
		"notification": kit.notification.calls,
	} {
		if calls != 1 {
			t.Errorf("%s service was called %d times, want 1", name, calls)
		}
	}
}

func TestCreateOrderReservesTheRightQuantity(t *testing.T) {
	kit := newTestKit()

	if _, err := kit.server.CreateOrder(context.Background(), &pb.CreateOrderRequest{
		CustomerId: "C001", ProductId: "P001", Quantity: 3,
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(kit.inventory.reserved) != 1 {
		t.Fatalf("%d reservations were made, want 1", len(kit.inventory.reserved))
	}
	if kit.inventory.reserved[0].GetQuantity() != 3 {
		t.Errorf("reserved %d units, want 3", kit.inventory.reserved[0].GetQuantity())
	}
	if kit.inventory.stock["P001"] != 7 {
		t.Errorf("stock left: got %d, want 7", kit.inventory.stock["P001"])
	}
}

func TestCreateOrderChargesTheComputedTotal(t *testing.T) {
	kit := newTestKit()

	if _, err := kit.server.CreateOrder(context.Background(), &pb.CreateOrderRequest{
		CustomerId: "C001", ProductId: "P003", Quantity: 2,
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(kit.payment.charged) != 1 {
		t.Fatalf("%d charges were made, want 1", len(kit.payment.charged))
	}
	if kit.payment.charged[0] != 3600 { // 1800 × 2
		t.Errorf("charged %v, want 3600", kit.payment.charged[0])
	}
}

func TestCreateOrderNotifiesTheCustomerWithOrderDetail(t *testing.T) {
	kit := newTestKit()

	if _, err := kit.server.CreateOrder(context.Background(), &pb.CreateOrderRequest{
		CustomerId: "C001", ProductId: "P002", Quantity: 2,
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(kit.notification.sent) != 1 {
		t.Fatalf("%d notifications were sent, want 1", len(kit.notification.sent))
	}

	notification := kit.notification.sent[0]
	if notification.GetCustomerId() != "C001" {
		t.Errorf("addressed to %q, want C001", notification.GetCustomerId())
	}
	for _, fragment := range []string{"Alice", "ORD0001", "Mechanical Keyboard"} {
		if !strings.Contains(notification.GetMessage(), fragment) {
			t.Errorf("message %q does not mention %q", notification.GetMessage(), fragment)
		}
	}
}

func TestOrderIDsIncrementAcrossOrders(t *testing.T) {
	kit := newTestKit()

	for i, want := range []string{"ORD0001", "ORD0002", "ORD0003"} {
		order, err := kit.server.CreateOrder(context.Background(), &pb.CreateOrderRequest{
			CustomerId: "C001", ProductId: "P002", Quantity: 1,
		})
		if err != nil {
			t.Fatalf("order %d failed: %v", i+1, err)
		}
		if order.GetOrderId() != want {
			t.Errorf("order %d: got %q, want %q", i+1, order.GetOrderId(), want)
		}
	}
}

// ---------- invalid input ----------

func TestCreateOrderRejectsInvalidRequests(t *testing.T) {
	cases := []struct {
		name    string
		request *pb.CreateOrderRequest
	}{
		{"no customer ID", &pb.CreateOrderRequest{ProductId: "P002", Quantity: 1}},
		{"no product ID", &pb.CreateOrderRequest{CustomerId: "C001", Quantity: 1}},
		{"neither ID", &pb.CreateOrderRequest{Quantity: 1}},
		{"zero quantity", &pb.CreateOrderRequest{CustomerId: "C001", ProductId: "P002", Quantity: 0}},
		{"negative quantity", &pb.CreateOrderRequest{CustomerId: "C001", ProductId: "P002", Quantity: -3}},
		{"nil request", nil},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			kit := newTestKit()

			_, err := kit.server.CreateOrder(context.Background(), testCase.request)

			assertCode(t, err, codes.InvalidArgument)

			// A malformed request must be rejected before any
			// dependency is troubled with it.
			if kit.customer.calls != 0 || kit.product.calls != 0 ||
				kit.inventory.calls != 0 || kit.payment.calls != 0 {
				t.Error("an invalid request still reached the dependencies")
			}
		})
	}
}

// ---------- a dependency rejects the request ----------

func TestCreateOrderFailsWhenTheCustomerDoesNotExist(t *testing.T) {
	kit := newTestKit()

	_, err := kit.server.CreateOrder(context.Background(), &pb.CreateOrderRequest{
		CustomerId: "C999", ProductId: "P002", Quantity: 1,
	})

	assertCode(t, err, codes.NotFound)

	// The customer is checked first, so nothing further should
	// have happened.
	if kit.product.calls != 0 {
		t.Error("the Product Service was called for an unknown customer")
	}
	if kit.inventory.calls != 0 {
		t.Error("stock was reserved for an unknown customer")
	}
	if kit.payment.calls != 0 {
		t.Error("a payment was attempted for an unknown customer")
	}
}

func TestCreateOrderFailsWhenTheProductDoesNotExist(t *testing.T) {
	kit := newTestKit()

	_, err := kit.server.CreateOrder(context.Background(), &pb.CreateOrderRequest{
		CustomerId: "C001", ProductId: "P999", Quantity: 1,
	})

	assertCode(t, err, codes.NotFound)

	if kit.inventory.calls != 0 {
		t.Error("stock was reserved for a product that does not exist")
	}
	if kit.payment.calls != 0 {
		t.Error("a payment was attempted for a product that does not exist")
	}
}

func TestCreateOrderFailsWhenStockIsInsufficient(t *testing.T) {
	kit := newTestKit()

	// P003 holds 2 units.
	_, err := kit.server.CreateOrder(context.Background(), &pb.CreateOrderRequest{
		CustomerId: "C001", ProductId: "P003", Quantity: 5,
	})

	assertCode(t, err, codes.ResourceExhausted)

	// Nothing should be charged for stock that was never
	// reserved.
	if kit.payment.calls != 0 {
		t.Error("the customer was charged despite the reservation failing")
	}
}

func TestCreateOrderFailsWhenPaymentIsDeclined(t *testing.T) {
	kit := newTestKit()
	kit.payment.declines = true

	_, err := kit.server.CreateOrder(context.Background(), &pb.CreateOrderRequest{
		CustomerId: "C001", ProductId: "P001", Quantity: 1,
	})

	assertCode(t, err, codes.FailedPrecondition)

	// No order should be recorded for a payment that failed.
	stream := &recordingOrderStream{}
	if err := kit.server.ListOrdersByCustomer(&pb.ListOrdersRequest{CustomerId: "C001"}, stream); err != nil {
		t.Fatalf("unexpected error listing orders: %v", err)
	}
	if len(stream.sent) != 0 {
		t.Errorf("%d orders were recorded despite the declined payment, want 0", len(stream.sent))
	}
}

// ---------- a dependency is unavailable ----------

func TestCreateOrderSurfacesAnUnavailableDependency(t *testing.T) {
	unavailable := status.Error(codes.Unavailable, "connection refused")

	cases := []struct {
		name   string
		break_ func(*testKit)
	}{
		{"customer service down", func(k *testKit) { k.customer.err = unavailable }},
		{"product service down", func(k *testKit) { k.product.err = unavailable }},
		{"inventory service down", func(k *testKit) { k.inventory.err = unavailable }},
		{"payment service down", func(k *testKit) { k.payment.err = unavailable }},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			kit := newTestKit()
			testCase.break_(kit)

			_, err := kit.server.CreateOrder(context.Background(), &pb.CreateOrderRequest{
				CustomerId: "C001", ProductId: "P002", Quantity: 1,
			})

			// The code has to reach the caller unchanged: the
			// gateway turns Unavailable into HTTP 503 and a
			// "service unavailable" message for the shopper.
			assertCode(t, err, codes.Unavailable)
		})
	}
}

func TestCreateOrderSurfacesATimedOutDependency(t *testing.T) {
	kit := newTestKit()
	kit.payment.err = status.Error(codes.DeadlineExceeded, "context deadline exceeded")

	_, err := kit.server.CreateOrder(context.Background(), &pb.CreateOrderRequest{
		CustomerId: "C001", ProductId: "P002", Quantity: 1,
	})

	assertCode(t, err, codes.DeadlineExceeded)
}

// ---------- notification is best-effort ----------

func TestOrderStillSucceedsWhenNotificationFails(t *testing.T) {
	kit := newTestKit()
	kit.notification.err = status.Error(codes.Unavailable, "notification service is down")

	order, err := kit.server.CreateOrder(context.Background(), &pb.CreateOrderRequest{
		CustomerId: "C001", ProductId: "P002", Quantity: 1,
	})

	// The customer has already paid at this point. Failing the
	// order because a confirmation message could not be sent
	// would be worse than sending no message.
	if err != nil {
		t.Fatalf("a notification failure should not fail a paid order: %v", err)
	}
	if order.GetStatus() != "CONFIRMED" {
		t.Errorf("status: got %q, want CONFIRMED", order.GetStatus())
	}
	if order.GetPaymentId() == "" {
		t.Error("the payment reference is missing from a confirmed order")
	}
}

// ---------- ListOrdersByCustomer ----------

func TestListOrdersReturnsOnlyThatCustomersOrders(t *testing.T) {
	kit := newTestKit()

	// Two orders for Alice, one for Bob.
	for i := 0; i < 2; i++ {
		if _, err := kit.server.CreateOrder(context.Background(), &pb.CreateOrderRequest{
			CustomerId: "C001", ProductId: "P002", Quantity: 1,
		}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if _, err := kit.server.CreateOrder(context.Background(), &pb.CreateOrderRequest{
		CustomerId: "C002", ProductId: "P001", Quantity: 1,
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	stream := &recordingOrderStream{}
	if err := kit.server.ListOrdersByCustomer(&pb.ListOrdersRequest{CustomerId: "C001"}, stream); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(stream.sent) != 2 {
		t.Fatalf("streamed %d orders, want 2", len(stream.sent))
	}
	for _, order := range stream.sent {
		if order.GetCustomerId() != "C001" {
			t.Errorf("another customer's order leaked into the results: %s", order.GetOrderId())
		}
	}
}

func TestListOrdersIsEmptyForACustomerWithNoOrders(t *testing.T) {
	kit := newTestKit()
	stream := &recordingOrderStream{}

	// No orders is a normal state, not an error.
	if err := kit.server.ListOrdersByCustomer(&pb.ListOrdersRequest{CustomerId: "C002"}, stream); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(stream.sent) != 0 {
		t.Errorf("streamed %d orders, want 0", len(stream.sent))
	}
}

func TestListOrdersRejectsAnEmptyCustomerID(t *testing.T) {
	kit := newTestKit()
	stream := &recordingOrderStream{}

	err := kit.server.ListOrdersByCustomer(&pb.ListOrdersRequest{CustomerId: ""}, stream)

	assertCode(t, err, codes.InvalidArgument)
}

func TestListOrdersPropagatesSendFailure(t *testing.T) {
	kit := newTestKit()

	if _, err := kit.server.CreateOrder(context.Background(), &pb.CreateOrderRequest{
		CustomerId: "C001", ProductId: "P002", Quantity: 1,
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	stream := &recordingOrderStream{err: status.Error(codes.Canceled, "client went away")}

	err := kit.server.ListOrdersByCustomer(&pb.ListOrdersRequest{CustomerId: "C001"}, stream)

	assertCode(t, err, codes.Canceled)
}
