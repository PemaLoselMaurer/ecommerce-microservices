package integration

import (
	"strings"
	"testing"
	"time"

	pb "ecommerce-microservices/proto"

	"ecommerce-microservices/internal/inventory"
	"ecommerce-microservices/internal/order"
	"ecommerce-microservices/internal/product"
	"ecommerce-microservices/resilience"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// The Order Service depends on five other services. These
// tests wire the real Order Service to real instances of those
// five over real gRPC connections, then work through each of
// the situations Part C asks for: the dependency available,
// unavailable, returning an error, and answering more slowly
// than the configured timeout.

// system is the Order Service plus the five services behind
// it, each individually reachable so a test can break one.
type system struct {
	order *order.Server

	customerSvc     *service
	productSvc      *service
	inventorySvc    *service
	paymentSvc      *service
	notificationSvc *service

	// pricing is the real calculate-order-price Worker handler,
	// served over HTTP.
	pricing *pricingWorker
}

// options tunes how the system under test is assembled.
type options struct {
	paymentDelay   time.Duration
	flakyInventory bool

	// interceptors mirrors the production wiring: the retry,
	// timeout and circuit-breaker interceptors the Order
	// Service really dials with.
	withResilience bool

	// paymentTimeout is the per-call budget on the payment
	// connection when resilience is enabled.
	paymentTimeout time.Duration

	fanOutTimeout time.Duration
}

func defaultOptions() options {
	return options{
		paymentDelay:   0,
		flakyInventory: false,
		withResilience: false,
		paymentTimeout: time.Second,
		fanOutTimeout:  order.DefaultFanOutTimeout,
	}
}

// newSystem starts all five dependencies and connects the real
// Order Service to them over real gRPC.
func newSystem(t *testing.T, opts options) *system {
	t.Helper()

	customerSvc := startCustomer(t)
	productSvc := startProduct(t, product.Catalogue())
	inventorySvc := startInventory(t, inventory.InitialStock(), opts.flakyInventory)
	paymentSvc := startPayment(t, opts.paymentDelay)
	notificationSvc := startNotification(t)

	var inventoryOpts, paymentOpts []grpc.DialOption
	if opts.withResilience {
		inventoryOpts = append(inventoryOpts, grpc.WithUnaryInterceptor(
			resilience.UnaryClientInterceptor(resilience.DefaultRetryConfig()),
		))
		paymentOpts = append(paymentOpts, grpc.WithUnaryInterceptor(
			resilience.TimeoutUnaryClientInterceptor(opts.paymentTimeout),
		))
	}

	pricingSvc := startPricing(t)

	orderServer := order.NewServer(order.Clients{
		Customer:     pb.NewCustomerServiceClient(customerSvc.dial(t)),
		Product:      pb.NewProductServiceClient(productSvc.dial(t)),
		Inventory:    pb.NewInventoryServiceClient(inventorySvc.dial(t, inventoryOpts...)),
		Payment:      pb.NewPaymentServiceClient(paymentSvc.dial(t, paymentOpts...)),
		Notification: pb.NewNotificationServiceClient(notificationSvc.dial(t)),
		Pricing:      pricingSvc.client(),
	}).WithFanOutTimeout(opts.fanOutTimeout)

	return &system{
		order:           orderServer,
		customerSvc:     customerSvc,
		productSvc:      productSvc,
		inventorySvc:    inventorySvc,
		paymentSvc:      paymentSvc,
		notificationSvc: notificationSvc,
		pricing:         pricingSvc,
	}
}

// ==========================================================
// 1. Every dependency is available
// ==========================================================

func TestOrderFlowAcrossAllFiveServices(t *testing.T) {
	sys := newSystem(t, defaultOptions())

	order, err := sys.order.CreateOrder(callContext(t), &pb.CreateOrderRequest{
		CustomerId: "C001",
		ProductId:  "P002",
		Quantity:   2,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The reply is assembled from four different services:
	// the name and price came from Product over the wire, the
	// payment reference from Payment, and the customer was
	// validated against Customer.
	if order.GetOrderId() != "ORD0001" {
		t.Errorf("order ID: got %q, want ORD0001", order.GetOrderId())
	}
	if order.GetProductName() != "Mechanical Keyboard" {
		t.Errorf("product name: got %q, want Mechanical Keyboard", order.GetProductName())
	}
	if order.GetTotalPrice() != 9000 { // 4500 × 2 from Product, priced by the Worker
		t.Errorf("total price: got %v, want 9000", order.GetTotalPrice())
	}
	if order.GetPaymentId() != "PAY0001" {
		t.Errorf("payment reference: got %q, want PAY0001", order.GetPaymentId())
	}
	if order.GetStatus() != "CONFIRMED" {
		t.Errorf("status: got %q, want CONFIRMED", order.GetStatus())
	}
}

func TestStockIsActuallyReservedInTheInventoryService(t *testing.T) {
	sys := newSystem(t, defaultOptions())

	stockClient := pb.NewInventoryServiceClient(sys.inventorySvc.dial(t))

	// P002 at 4,500 each stays under the Payment Service's
	// decline threshold, so this test fails only if the
	// reservation itself goes wrong.
	before, err := stockClient.GetStock(callContext(t), &pb.GetStockRequest{ProductId: "P002"})
	if err != nil {
		t.Fatalf("unexpected error reading stock: %v", err)
	}

	if _, err := sys.order.CreateOrder(callContext(t), &pb.CreateOrderRequest{
		CustomerId: "C001", ProductId: "P002", Quantity: 3,
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Reading the level back through a separate connection
	// proves the reservation crossed the wire and changed the
	// Inventory Service's own state.
	after, err := stockClient.GetStock(callContext(t), &pb.GetStockRequest{ProductId: "P002"})
	if err != nil {
		t.Fatalf("unexpected error reading stock: %v", err)
	}

	if after.GetQuantity() != before.GetQuantity()-3 {
		t.Errorf("stock went from %d to %d, want a drop of 3",
			before.GetQuantity(), after.GetQuantity())
	}
}

func TestOrderReachesTheNotificationServiceSubscriber(t *testing.T) {
	sys := newSystem(t, defaultOptions())

	notifications := pb.NewNotificationServiceClient(sys.notificationSvc.dial(t))

	ctx := callContext(t)
	stream, err := notifications.SubscribeNotifications(ctx, &pb.SubscribeRequest{CustomerId: "C001"})
	if err != nil {
		t.Fatalf("could not subscribe: %v", err)
	}

	// Give the subscription a moment to register before the
	// order fires the notification.
	time.Sleep(100 * time.Millisecond)

	if _, err := sys.order.CreateOrder(callContext(t), &pb.CreateOrderRequest{
		CustomerId: "C001", ProductId: "P002", Quantity: 1,
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	received := make(chan *pb.Notification, 1)
	go func() {
		notification, err := stream.Recv()
		if err == nil {
			received <- notification
		}
		close(received)
	}()

	select {
	case notification := <-received:
		if notification == nil {
			t.Fatal("the notification stream closed without delivering anything")
		}
		if notification.GetCustomerId() != "C001" {
			t.Errorf("addressed to %q, want C001", notification.GetCustomerId())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no notification arrived over the stream")
	}
}

// ==========================================================
// 2. A range of valid and invalid identifiers
// ==========================================================

func TestOrderAcrossServicesForARangeOfIdentifiers(t *testing.T) {
	cases := []struct {
		name       string
		customerID string
		productID  string
		quantity   int32
		wantCode   codes.Code // OK means the order should succeed
		wantTotal  float64
	}{
		// Valid combinations. Unit prices come from the Product
		// Service and totals from the pricing Worker.
		{"laptop for Alice", "C001", "P001", 1, codes.OK, 75000},
		{"keyboards for Bob", "C002", "P002", 2, codes.OK, 9000},
		{"mouse for Alice, 5% bulk discount", "C001", "P003", 4, codes.OK, 6840},
		{"one keyboard pays delivery", "C002", "P002", 1, codes.OK, 4650},

		// Invalid identifiers, each rejected by the service that
		// owns that record.
		{"unknown customer", "C999", "P001", 1, codes.NotFound, 0},
		{"unknown product", "C001", "P999", 1, codes.NotFound, 0},
		{"lower-case customer ID", "c001", "P001", 1, codes.NotFound, 0},
		{"lower-case product ID", "C001", "p001", 1, codes.NotFound, 0},
		{"empty customer ID", "", "P001", 1, codes.InvalidArgument, 0},
		{"empty product ID", "C001", "", 1, codes.InvalidArgument, 0},
		{"zero quantity", "C001", "P001", 0, codes.InvalidArgument, 0},
		{"negative quantity", "C001", "P001", -1, codes.InvalidArgument, 0},

		// Valid identifiers, but the warehouse cannot fill it.
		{"more than is stocked", "C001", "P004", 9, codes.ResourceExhausted, 0},

		// Valid identifiers, but the pricing Worker refuses the
		// order as above its limit — before any stock is held.
		{"above the pricing limit", "C001", "P001", 20, codes.FailedPrecondition, 0},
		{"an item that is sold out", "C001", "P008", 1, codes.ResourceExhausted, 0},

		// Valid identifiers, but the payment is declined: the
		// Product Service prices this above the Payment
		// Service's limit.
		{"over the payment limit", "C001", "P001", 2, codes.FailedPrecondition, 0},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			sys := newSystem(t, defaultOptions())

			placed, err := sys.order.CreateOrder(callContext(t), &pb.CreateOrderRequest{
				CustomerId: testCase.customerID,
				ProductId:  testCase.productID,
				Quantity:   testCase.quantity,
			})

			if testCase.wantCode == codes.OK {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if placed.GetTotalPrice() != testCase.wantTotal {
					t.Errorf("total: got %v, want %v", placed.GetTotalPrice(), testCase.wantTotal)
				}
				return
			}

			assertCode(t, err, testCase.wantCode)
		})
	}
}

// ==========================================================
// 3. The dependency is unavailable
// ==========================================================

func TestOrderReportsEachDependencyBeingUnavailable(t *testing.T) {
	cases := []struct {
		name string
		stop func(*system)
	}{
		{"Customer Service stopped", func(s *system) { s.customerSvc.stop() }},
		{"Product Service stopped", func(s *system) { s.productSvc.stop() }},
		{"Inventory Service stopped", func(s *system) { s.inventorySvc.stop() }},
		{"Payment Service stopped", func(s *system) { s.paymentSvc.stop() }},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			sys := newSystem(t, defaultOptions())

			// Shutting the gRPC server down is the closest an
			// in-process test gets to killing the process: the
			// connection breaks and calls fail the same way.
			testCase.stop(sys)

			_, err := sys.order.CreateOrder(callContext(t), &pb.CreateOrderRequest{
				CustomerId: "C001", ProductId: "P002", Quantity: 1,
			})

			// Unavailable is what the gateway turns into HTTP 503
			// and a "service unavailable" message for the shopper,
			// so the code has to survive the trip unchanged.
			assertCode(t, err, codes.Unavailable)
		})
	}
}

func TestOrderStillSucceedsWhenOnlyNotificationIsDown(t *testing.T) {
	sys := newSystem(t, defaultOptions())

	sys.notificationSvc.stop()

	// The customer has already paid by the time the
	// notification is attempted, so a confirmation that cannot
	// be sent must not undo the order.
	placed, err := sys.order.CreateOrder(callContext(t), &pb.CreateOrderRequest{
		CustomerId: "C001", ProductId: "P002", Quantity: 1,
	})
	if err != nil {
		t.Fatalf("a notification failure should not fail a paid order: %v", err)
	}

	if placed.GetStatus() != "CONFIRMED" {
		t.Errorf("status: got %q, want CONFIRMED", placed.GetStatus())
	}
	if placed.GetPaymentId() == "" {
		t.Error("the payment reference is missing from a confirmed order")
	}
}

func TestNothingIsChargedWhenAnEarlierDependencyIsDown(t *testing.T) {
	sys := newSystem(t, defaultOptions())

	sys.productSvc.stop()

	_, err := sys.order.CreateOrder(callContext(t), &pb.CreateOrderRequest{
		CustomerId: "C001", ProductId: "P002", Quantity: 1,
	})
	assertCode(t, err, codes.Unavailable)

	// Stock is checked after the product, so nothing should
	// have been taken off the shelf either.
	stockClient := pb.NewInventoryServiceClient(sys.inventorySvc.dial(t))
	item, err := stockClient.GetStock(callContext(t), &pb.GetStockRequest{ProductId: "P002"})
	if err != nil {
		t.Fatalf("unexpected error reading stock: %v", err)
	}

	if item.GetQuantity() != inventory.InitialStock()["P002"] {
		t.Errorf("stock changed to %d despite the order failing", item.GetQuantity())
	}
}

// ==========================================================
// 4. The dependency returns an error
// ==========================================================

func TestOrderPropagatesEachDependencysOwnError(t *testing.T) {
	cases := []struct {
		name       string
		customerID string
		productID  string
		quantity   int32
		wantCode   codes.Code
		wantFrom   string
	}{
		{"Customer Service reports NotFound", "C999", "P002", 1, codes.NotFound, "Customer"},
		{"Product Service reports NotFound", "C001", "P999", 1, codes.NotFound, "Product"},
		{"Inventory Service reports ResourceExhausted", "C001", "P008", 1, codes.ResourceExhausted, "Inventory"},
		{"Payment Service reports FailedPrecondition", "C001", "P001", 2, codes.FailedPrecondition, "Payment"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			sys := newSystem(t, defaultOptions())

			_, err := sys.order.CreateOrder(callContext(t), &pb.CreateOrderRequest{
				CustomerId: testCase.customerID,
				ProductId:  testCase.productID,
				Quantity:   testCase.quantity,
			})

			assertCode(t, err, testCase.wantCode)

			// The dependency's own wording has to survive the
			// hop, since the gateway shows it to the customer.
			st, _ := status.FromError(err)
			if st.Message() == "" {
				t.Errorf("the error from the %s Service arrived with no message", testCase.wantFrom)
			}
		})
	}
}

func TestADeclinedPaymentLeavesNoOrderBehind(t *testing.T) {
	sys := newSystem(t, defaultOptions())

	// Two laptops price above the Payment Service's limit.
	_, err := sys.order.CreateOrder(callContext(t), &pb.CreateOrderRequest{
		CustomerId: "C001", ProductId: "P001", Quantity: 2,
	})
	assertCode(t, err, codes.FailedPrecondition)

	stream := &collectingStream{}
	if err := sys.order.ListOrdersByCustomer(&pb.ListOrdersRequest{CustomerId: "C001"}, stream); err != nil {
		t.Fatalf("unexpected error listing orders: %v", err)
	}

	if len(stream.orders) != 0 {
		t.Errorf("%d orders were recorded despite the declined payment", len(stream.orders))
	}
}

// ==========================================================
// 5. The dependency is slower than the configured timeout
// ==========================================================

func TestSlowPaymentIsCutOffByTheTimeoutInterceptor(t *testing.T) {
	opts := defaultOptions()
	opts.withResilience = true
	opts.paymentDelay = 3 * time.Second // the simulated slow gateway
	opts.paymentTimeout = 300 * time.Millisecond
	opts.fanOutTimeout = 5 * time.Second

	sys := newSystem(t, opts)

	start := time.Now()
	_, err := sys.order.CreateOrder(callContext(t), &pb.CreateOrderRequest{
		CustomerId: "C001", ProductId: "P002", Quantity: 1,
	})
	elapsed := time.Since(start)

	assertCode(t, err, codes.DeadlineExceeded)

	// The point of the pattern is failing fast: the call must
	// end on its own budget, not after the dependency finally
	// answers three seconds later.
	if elapsed > 2*time.Second {
		t.Errorf("took %s to give up on a 300ms budget", elapsed)
	}
}

func TestAPaymentInsideTheBudgetStillSucceeds(t *testing.T) {
	opts := defaultOptions()
	opts.withResilience = true
	opts.paymentDelay = 100 * time.Millisecond
	opts.paymentTimeout = time.Second

	sys := newSystem(t, opts)

	// A dependency that is merely slow, not too slow, must not
	// be cut off — a timeout that fires early is as much a bug
	// as one that never fires.
	placed, err := sys.order.CreateOrder(callContext(t), &pb.CreateOrderRequest{
		CustomerId: "C001", ProductId: "P002", Quantity: 1,
	})
	if err != nil {
		t.Fatalf("a payment well inside the budget was cut off: %v", err)
	}
	if placed.GetPaymentId() == "" {
		t.Error("the order came back without a payment reference")
	}
}

// ==========================================================
// 6. Retry across a real connection
// ==========================================================

func TestRetryRecoversFromTheFlakyInventoryService(t *testing.T) {
	opts := defaultOptions()
	opts.withResilience = true
	opts.flakyInventory = true // fails two calls in three

	sys := newSystem(t, opts)

	// Without the retry interceptor this order would fail on
	// the first ReserveStock. With it, the third attempt gets
	// through and the customer never sees the hiccup.
	placed, err := sys.order.CreateOrder(callContext(t), &pb.CreateOrderRequest{
		CustomerId: "C001", ProductId: "P002", Quantity: 1,
	})
	if err != nil {
		t.Fatalf("the retry interceptor did not recover the order: %v", err)
	}

	if placed.GetStatus() != "CONFIRMED" {
		t.Errorf("status: got %q, want CONFIRMED", placed.GetStatus())
	}
}

func TestWithoutRetryTheFlakyInventoryServiceFailsTheOrder(t *testing.T) {
	opts := defaultOptions()
	opts.withResilience = false // no interceptors on the connection
	opts.flakyInventory = true

	sys := newSystem(t, opts)

	// The contrast is the point: the same flaky dependency
	// fails the order outright when nothing retries.
	_, err := sys.order.CreateOrder(callContext(t), &pb.CreateOrderRequest{
		CustomerId: "C001", ProductId: "P002", Quantity: 1,
	})

	assertCode(t, err, codes.Unavailable)
}

// ==========================================================
// 7. Circuit breaker across a real connection
// ==========================================================

func TestBreakerOpensAgainstADeadDependencyAndStopsCalling(t *testing.T) {
	customerSvc := startCustomer(t)
	productSvc := startProduct(t, product.Catalogue())
	inventorySvc := startInventory(t, inventory.InitialStock(), false)
	paymentSvc := startPayment(t, 0)
	notificationSvc := startNotification(t)

	breaker := resilience.NewCircuitBreaker("product-service", resilience.CircuitBreakerConfig{
		FailureThreshold: 3,
		ResetTimeout:     200 * time.Millisecond,
	})

	orderServer := order.NewServer(order.Clients{
		Customer:  pb.NewCustomerServiceClient(customerSvc.dial(t)),
		Product:   pb.NewProductServiceClient(productSvc.dial(t, grpc.WithUnaryInterceptor(breaker.UnaryClientInterceptor()))),
		Inventory: pb.NewInventoryServiceClient(inventorySvc.dial(t)),
		Payment:   pb.NewPaymentServiceClient(paymentSvc.dial(t)),

		Notification: pb.NewNotificationServiceClient(notificationSvc.dial(t)),
		Pricing:      startPricing(t).client(),
	}).WithFanOutTimeout(2 * time.Second)

	// The Product Service dies.
	productSvc.stop()

	// Three consecutive failures trip the breaker.
	for i := 0; i < 3; i++ {
		if _, err := orderServer.CreateOrder(callContext(t), &pb.CreateOrderRequest{
			CustomerId: "C001", ProductId: "P002", Quantity: 1,
		}); err == nil {
			t.Fatalf("order %d should have failed against a dead Product Service", i+1)
		}
	}

	// The fourth is rejected by the breaker itself, before the
	// network is touched — which is why it comes back almost
	// instantly.
	start := time.Now()
	_, err := orderServer.CreateOrder(callContext(t), &pb.CreateOrderRequest{
		CustomerId: "C001", ProductId: "P002", Quantity: 1,
	})
	elapsed := time.Since(start)

	assertCode(t, err, codes.Unavailable)

	st, _ := status.FromError(err)
	if !strings.Contains(st.Message(), "circuit breaker") {
		t.Errorf("expected the breaker to reject the call, got %q", st.Message())
	}
	if elapsed > 200*time.Millisecond {
		t.Errorf("the open breaker took %s to reject a call", elapsed)
	}
}

// ---------- helpers ----------

// collectingStream captures orders streamed by
// ListOrdersByCustomer.
type collectingStream struct {
	grpc.ServerStream
	orders []*pb.Order
}

func (s *collectingStream) Send(order *pb.Order) error {
	s.orders = append(s.orders, order)
	return nil
}
