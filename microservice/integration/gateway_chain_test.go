package integration

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	pb "ecommerce-microservices/proto"

	"ecommerce-microservices/internal/inventory"
	"ecommerce-microservices/internal/order"
	"ecommerce-microservices/internal/product"
	"ecommerce-microservices/resilience"

	"google.golang.org/grpc"
)

// The gateway is the deepest caller in the system: a single
// HTTP request from the browser fans out through the Order
// Service to all five services behind it. These tests drive
// the real gateway binary's HTTP handler over real gRPC
// connections to real services, so a failure anywhere in that
// chain is exercised exactly as it would be in the running
// application.
//
// The gateway's own package is a main package and cannot be
// imported, so the chain is assembled here from the same
// pieces its main() wires together, and its HTTP surface is
// reproduced by the small router below. What is under test is
// the service-to-service communication; the gateway's own
// handler logic has its own tests in gateway-service.

// chain is the whole system: an HTTP endpoint at the top and
// six services underneath.
type chain struct {
	handler http.Handler

	productSvc      *service
	customerSvc     *service
	inventorySvc    *service
	paymentSvc      *service
	notificationSvc *service
	orderSvc        *service
}

// newChain starts every service and connects them the way the
// running application does.
func newChain(t *testing.T, opts options) *chain {
	t.Helper()

	customerSvc := startCustomer(t)
	productSvc := startProduct(t, product.Catalogue())
	inventorySvc := startInventory(t, inventory.InitialStock(), opts.flakyInventory)
	paymentSvc := startPayment(t, opts.paymentDelay)
	notificationSvc := startNotification(t)

	// The Order Service, wired to the five below it with the
	// same resilience interceptors main() uses.
	var inventoryOpts, paymentOpts []grpc.DialOption
	if opts.withResilience {
		inventoryOpts = append(inventoryOpts, grpc.WithUnaryInterceptor(
			resilience.UnaryClientInterceptor(resilience.DefaultRetryConfig()),
		))
		paymentOpts = append(paymentOpts, grpc.WithUnaryInterceptor(
			resilience.TimeoutUnaryClientInterceptor(opts.paymentTimeout),
		))
	}

	orderImpl := order.NewServer(order.Clients{
		Customer:     pb.NewCustomerServiceClient(customerSvc.dial(t)),
		Product:      pb.NewProductServiceClient(productSvc.dial(t)),
		Inventory:    pb.NewInventoryServiceClient(inventorySvc.dial(t, inventoryOpts...)),
		Payment:      pb.NewPaymentServiceClient(paymentSvc.dial(t, paymentOpts...)),
		Notification: pb.NewNotificationServiceClient(notificationSvc.dial(t)),
		Pricing:      startPricing(t).client(),
	}).WithFanOutTimeout(opts.fanOutTimeout)

	// The Order Service itself is served over gRPC, so the
	// gateway reaches it across a connection rather than by
	// calling into it directly.
	orderSvc := startService(t, "Order Service", func(s *grpc.Server) {
		pb.RegisterOrderServiceServer(s, orderImpl)
	})

	front := &frontEnd{
		products:  pb.NewProductServiceClient(productSvc.dial(t)),
		inventory: pb.NewInventoryServiceClient(inventorySvc.dial(t)),
		orders:    pb.NewOrderServiceClient(orderSvc.dial(t)),
		customers: pb.NewCustomerServiceClient(customerSvc.dial(t)),
	}

	return &chain{
		handler:         front.routes(),
		productSvc:      productSvc,
		customerSvc:     customerSvc,
		inventorySvc:    inventorySvc,
		paymentSvc:      paymentSvc,
		notificationSvc: notificationSvc,
		orderSvc:        orderSvc,
	}
}

func (c *chain) get(target string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, target, nil)
	recorder := httptest.NewRecorder()
	c.handler.ServeHTTP(recorder, request)
	return recorder
}

func (c *chain) post(target, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, target, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	c.handler.ServeHTTP(recorder, request)
	return recorder
}

// ==========================================================
// The chain works end to end
// ==========================================================

func TestBrowserRequestReachesTheProductAndInventoryServices(t *testing.T) {
	c := newChain(t, defaultOptions())

	response := c.get("/api/products/P001")

	if response.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 (%s)", response.Code, response.Body.String())
	}

	var body map[string]any
	_ = json.Unmarshal(response.Body.Bytes(), &body)

	// The name came from the Product Service and the stock
	// level from the Inventory Service, over two separate gRPC
	// connections, merged into one HTTP response.
	if body["name"] != "Laptop" {
		t.Errorf("name: got %v, want Laptop", body["name"])
	}
	if body["stock"] != float64(10) {
		t.Errorf("stock: got %v, want 10", body["stock"])
	}
}

func TestBrowserCheckoutTravelsThroughAllSixServices(t *testing.T) {
	c := newChain(t, defaultOptions())

	response := c.post("/api/orders", `{"customer_id":"C001","product_id":"P002","quantity":2}`)

	if response.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201 (%s)", response.Code, response.Body.String())
	}

	var placed map[string]any
	_ = json.Unmarshal(response.Body.Bytes(), &placed)

	if placed["order_id"] != "ORD0001" {
		t.Errorf("order ID: got %v, want ORD0001", placed["order_id"])
	}
	if placed["product_name"] != "Mechanical Keyboard" {
		t.Errorf("product name: got %v, want Mechanical Keyboard", placed["product_name"])
	}
	if placed["payment_id"] != "PAY0001" {
		t.Errorf("payment reference: got %v, want PAY0001", placed["payment_id"])
	}
	if placed["total_price"] != float64(9000) {
		t.Errorf("total: got %v, want 9000", placed["total_price"])
	}
}

func TestCatalogueStreamsThroughTheGateway(t *testing.T) {
	c := newChain(t, defaultOptions())

	response := c.get("/api/products")

	if response.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", response.Code)
	}

	var products []map[string]any
	_ = json.Unmarshal(response.Body.Bytes(), &products)

	// The Product Service streams these one at a time; the
	// gateway collects the stream and enriches each entry with
	// a stock level from Inventory.
	if len(products) != len(product.Catalogue()) {
		t.Fatalf("got %d products, want %d", len(products), len(product.Catalogue()))
	}
	for _, entry := range products {
		if _, hasStock := entry["stock"]; !hasStock && entry["product_id"] != "P008" {
			t.Errorf("%v came back without a stock level", entry["product_id"])
		}
	}
}

func TestOrderHistoryStreamsBackThroughTheGateway(t *testing.T) {
	c := newChain(t, defaultOptions())

	for i := 0; i < 2; i++ {
		if response := c.post("/api/orders",
			`{"customer_id":"C001","product_id":"P003","quantity":1}`); response.Code != http.StatusCreated {
			t.Fatalf("order %d failed: %s", i+1, response.Body.String())
		}
	}

	response := c.get("/api/orders?customer_id=C001")

	if response.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", response.Code)
	}

	var orders []map[string]any
	_ = json.Unmarshal(response.Body.Bytes(), &orders)

	if len(orders) != 2 {
		t.Fatalf("got %d orders, want 2", len(orders))
	}
	for _, placed := range orders {
		if placed["payment_id"] == "" {
			t.Error("an order came back without its payment reference")
		}
	}
}

// ==========================================================
// A break anywhere in the chain surfaces as the right HTTP
// status at the top
// ==========================================================

func TestEachStoppedServiceSurfacesAsTheRightHTTPStatus(t *testing.T) {
	cases := []struct {
		name       string
		stop       func(*chain)
		request    func(*chain) *httptest.ResponseRecorder
		wantStatus int
	}{
		{
			name: "Product Service stopped, browsing a product",
			stop: func(c *chain) { c.productSvc.stop() },
			request: func(c *chain) *httptest.ResponseRecorder {
				return c.get("/api/products/P001")
			},
			wantStatus: http.StatusServiceUnavailable,
		},
		{
			name: "Product Service stopped, browsing the catalogue",
			stop: func(c *chain) { c.productSvc.stop() },
			request: func(c *chain) *httptest.ResponseRecorder {
				return c.get("/api/products")
			},
			wantStatus: http.StatusServiceUnavailable,
		},
		{
			name: "Order Service stopped, placing an order",
			stop: func(c *chain) { c.orderSvc.stop() },
			request: func(c *chain) *httptest.ResponseRecorder {
				return c.post("/api/orders", `{"customer_id":"C001","product_id":"P002","quantity":1}`)
			},
			wantStatus: http.StatusServiceUnavailable,
		},
		{
			// Two hops down: the gateway can reach the Order
			// Service, but the Order Service cannot reach
			// Payment. The failure still has to arrive as 503.
			name: "Payment Service stopped, two hops below the browser",
			stop: func(c *chain) { c.paymentSvc.stop() },
			request: func(c *chain) *httptest.ResponseRecorder {
				return c.post("/api/orders", `{"customer_id":"C001","product_id":"P002","quantity":1}`)
			},
			wantStatus: http.StatusServiceUnavailable,
		},
		{
			name: "Customer Service stopped, two hops below the browser",
			stop: func(c *chain) { c.customerSvc.stop() },
			request: func(c *chain) *httptest.ResponseRecorder {
				return c.post("/api/orders", `{"customer_id":"C001","product_id":"P002","quantity":1}`)
			},
			wantStatus: http.StatusServiceUnavailable,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			c := newChain(t, defaultOptions())
			testCase.stop(c)

			response := testCase.request(c)

			if response.Code != testCase.wantStatus {
				t.Fatalf("status: got %d, want %d (%s)",
					response.Code, testCase.wantStatus, response.Body.String())
			}

			// The shopper gets an explanation, not a stack trace.
			body := response.Body.String()
			if strings.Contains(body, "transport:") || strings.Contains(body, "bufnet") {
				t.Errorf("the response leaks transport detail: %s", body)
			}
		})
	}
}

func TestCatalogueStillLoadsWhenOnlyInventoryIsDown(t *testing.T) {
	c := newChain(t, defaultOptions())

	c.inventorySvc.stop()

	response := c.get("/api/products")

	// Losing stock levels degrades the storefront; it must not
	// take it down.
	if response.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 (%s)", response.Code, response.Body.String())
	}

	var products []map[string]any
	_ = json.Unmarshal(response.Body.Bytes(), &products)

	if len(products) == 0 {
		t.Fatal("the catalogue came back empty")
	}
	for _, entry := range products {
		if _, hasStock := entry["stock"]; hasStock {
			t.Errorf("%v reported a stock level with Inventory stopped", entry["product_id"])
		}
		if entry["stock_error"] == nil {
			t.Errorf("%v has no explanation for the missing stock level", entry["product_id"])
		}
	}
}

func TestSlowPaymentSurfacesAsAGatewayTimeout(t *testing.T) {
	opts := defaultOptions()
	opts.withResilience = true
	opts.paymentDelay = 3 * time.Second
	opts.paymentTimeout = 300 * time.Millisecond
	opts.fanOutTimeout = 5 * time.Second

	c := newChain(t, opts)

	start := time.Now()
	response := c.post("/api/orders", `{"customer_id":"C001","product_id":"P002","quantity":1}`)
	elapsed := time.Since(start)

	// Three hops from the browser, a timeout deep in the system
	// has to arrive as 504 rather than hanging the page.
	if response.Code != http.StatusGatewayTimeout {
		t.Fatalf("status: got %d, want 504 (%s)", response.Code, response.Body.String())
	}
	if elapsed > 2*time.Second {
		t.Errorf("the browser waited %s for a 300ms budget", elapsed)
	}

	if !strings.Contains(response.Body.String(), "too long") {
		t.Errorf("the message does not explain the timeout: %s", response.Body.String())
	}
}

func TestRetryHidesTheFlakyInventoryServiceFromTheBrowser(t *testing.T) {
	opts := defaultOptions()
	opts.withResilience = true
	opts.flakyInventory = true

	c := newChain(t, opts)

	// Two of every three ReserveStock calls fail inside the
	// system. The shopper should never find out.
	response := c.post("/api/orders", `{"customer_id":"C001","product_id":"P002","quantity":1}`)

	if response.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201 (%s)", response.Code, response.Body.String())
	}
}

func TestBusinessFailuresKeepTheirOwnStatusThroughTheChain(t *testing.T) {
	cases := []struct {
		name       string
		body       string
		wantStatus int
		wantText   string
	}{
		{
			name:       "unknown product",
			body:       `{"customer_id":"C001","product_id":"P999","quantity":1}`,
			wantStatus: http.StatusNotFound,
			wantText:   "P999",
		},
		{
			name:       "unknown customer",
			body:       `{"customer_id":"C999","product_id":"P002","quantity":1}`,
			wantStatus: http.StatusNotFound,
			wantText:   "C999",
		},
		{
			name:       "out of stock",
			body:       `{"customer_id":"C001","product_id":"P008","quantity":1}`,
			wantStatus: http.StatusConflict,
			wantText:   "stock",
		},
		{
			name:       "payment declined",
			body:       `{"customer_id":"C001","product_id":"P001","quantity":2}`,
			wantStatus: http.StatusConflict,
			wantText:   "declined",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			c := newChain(t, defaultOptions())

			response := c.post("/api/orders", testCase.body)

			if response.Code != testCase.wantStatus {
				t.Fatalf("status: got %d, want %d (%s)",
					response.Code, testCase.wantStatus, response.Body.String())
			}
			if !strings.Contains(response.Body.String(), testCase.wantText) {
				t.Errorf("the message lost the detail from the service: %s", response.Body.String())
			}
		})
	}
}
