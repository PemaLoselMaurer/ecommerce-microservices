package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	pb "ecommerce-microservices/proto"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// These tests drive the gateway's HTTP handlers through
// httptest with test doubles behind them. No gRPC server
// listens and no service runs, so they cover the gateway's own
// job: translating HTTP to gRPC, mapping status codes, and
// turning service errors into messages a shopper can read.

// ---------- fixtures ----------

type gatewayKit struct {
	gateway   *Gateway
	handler   http.Handler
	product   *fakeProductClient
	customer  *fakeCustomerClient
	inventory *fakeInventoryClient
	order     *fakeOrderClient
}

func newGatewayKit() *gatewayKit {
	product := &fakeProductClient{
		order: []string{"P001", "P002", "P003"},
		products: map[string]*pb.Product{
			"P001": {ProductId: "P001", Name: "Laptop", Price: 75000, ListPrice: 88000,
				Category: "laptops", Badge: "BESTSELLER", Rating: 4.6, ReviewCount: 214},
			"P002": {ProductId: "P002", Name: "Mechanical Keyboard", Price: 4500, Category: "accessories"},
			"P003": {ProductId: "P003", Name: "Wireless Mouse", Price: 1800, Category: "accessories"},
		},
	}

	customer := &fakeCustomerClient{
		password: "alice1234",
		customers: map[string]*pb.Customer{
			"C001": {CustomerId: "C001", Name: "Alice Nguyen",
				Email: "alice@example.com", Address: "123 Market St"},
		},
		credentials: map[string]string{"alice@example.com": "C001"},
	}

	inventory := &fakeInventoryClient{stock: map[string]int32{"P001": 10, "P002": 25, "P003": 0}}

	order := &fakeOrderClient{ordersByCustomer: map[string][]*pb.Order{}}

	gateway := NewGateway(product, customer, inventory, order, 2*time.Second)

	return &gatewayKit{
		gateway:   gateway,
		handler:   gateway.Routes(),
		product:   product,
		customer:  customer,
		inventory: inventory,
		order:     order,
	}
}

// do sends a request through the gateway and returns the
// recorded response.
func (k *gatewayKit) do(method, target, body string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}

	request := httptest.NewRequest(method, target, reader)
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}

	recorder := httptest.NewRecorder()
	k.handler.ServeHTTP(recorder, request)
	return recorder
}

// signIn signs in as Alice and returns the session cookie.
func (k *gatewayKit) signIn(t *testing.T) *http.Cookie {
	t.Helper()

	response := k.do(http.MethodPost, "/api/auth/login",
		`{"email":"alice@example.com","password":"alice1234"}`)

	if response.Code != http.StatusOK {
		t.Fatalf("sign-in failed: %d %s", response.Code, response.Body.String())
	}

	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == sessionCookie {
			return cookie
		}
	}

	t.Fatal("no session cookie was set on a successful sign-in")
	return nil
}

func decodeError(t *testing.T, recorder *httptest.ResponseRecorder) errorView {
	t.Helper()

	var view errorView
	if err := json.Unmarshal(recorder.Body.Bytes(), &view); err != nil {
		t.Fatalf("response body is not an error object: %v (%s)", err, recorder.Body.String())
	}
	return view
}

// ==========================================================
// Product lookup
// ==========================================================

func TestGetProductReturnsProductWithStock(t *testing.T) {
	kit := newGatewayKit()

	response := kit.do(http.MethodGet, "/api/products/P001", "")

	if response.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 (%s)", response.Code, response.Body.String())
	}

	var product productView
	if err := json.Unmarshal(response.Body.Bytes(), &product); err != nil {
		t.Fatalf("could not decode the response: %v", err)
	}

	if product.ProductID != "P001" {
		t.Errorf("product ID: got %q, want %q", product.ProductID, "P001")
	}
	if product.Name != "Laptop" {
		t.Errorf("name: got %q, want %q", product.Name, "Laptop")
	}
	if product.Price != 75000 {
		t.Errorf("price: got %v, want 75000", product.Price)
	}
	if product.ListPrice != 88000 {
		t.Errorf("list price: got %v, want 88000", product.ListPrice)
	}
	if product.Badge != "BESTSELLER" {
		t.Errorf("badge: got %q, want %q", product.Badge, "BESTSELLER")
	}
	// Stock comes from a different service and is merged in.
	if product.Stock == nil || *product.Stock != 10 {
		t.Errorf("stock: got %v, want 10", product.Stock)
	}
}

func TestGetProductReturnsTheRightRecordForEachID(t *testing.T) {
	kit := newGatewayKit()

	cases := []struct {
		productID string
		wantName  string
		wantStock int32
	}{
		{"P001", "Laptop", 10},
		{"P002", "Mechanical Keyboard", 25},
		{"P003", "Wireless Mouse", 0},
	}

	for _, testCase := range cases {
		t.Run(testCase.productID, func(t *testing.T) {
			response := kit.do(http.MethodGet, "/api/products/"+testCase.productID, "")

			if response.Code != http.StatusOK {
				t.Fatalf("status: got %d, want 200", response.Code)
			}

			var product productView
			_ = json.Unmarshal(response.Body.Bytes(), &product)

			if product.Name != testCase.wantName {
				t.Errorf("name: got %q, want %q", product.Name, testCase.wantName)
			}
			if product.Stock == nil || *product.Stock != testCase.wantStock {
				t.Errorf("stock: got %v, want %d", product.Stock, testCase.wantStock)
			}
		})
	}
}

func TestGetProductReturns404ForUnknownID(t *testing.T) {
	kit := newGatewayKit()

	response := kit.do(http.MethodGet, "/api/products/P999", "")

	if response.Code != http.StatusNotFound {
		t.Fatalf("status: got %d, want 404", response.Code)
	}

	view := decodeError(t, response)
	if view.Code != "NotFound" {
		t.Errorf("code: got %q, want %q", view.Code, "NotFound")
	}
	// The message is displayed to the shopper, so it must read
	// as a sentence and name what was missing.
	if !strings.Contains(view.Error, "P999") {
		t.Errorf("message %q does not name the missing product", view.Error)
	}
	if !strings.HasSuffix(view.Error, ".") {
		t.Errorf("message %q is not a complete sentence", view.Error)
	}
}

func TestGetProductReturns400ForAnEmptyID(t *testing.T) {
	kit := newGatewayKit()

	// A trailing slash carries no identifier at all.
	response := kit.do(http.MethodGet, "/api/products/", "")

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400", response.Code)
	}

	view := decodeError(t, response)
	if view.Code != "INVALID_ARGUMENT" {
		t.Errorf("code: got %q, want %q", view.Code, "INVALID_ARGUMENT")
	}
}

func TestGetProductReturns503WhenTheProductServiceIsDown(t *testing.T) {
	kit := newGatewayKit()
	kit.product.getErr = status.Error(codes.Unavailable, "connection refused")

	response := kit.do(http.MethodGet, "/api/products/P001", "")

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status: got %d, want 503", response.Code)
	}

	view := decodeError(t, response)
	if view.Service != "Product Service" {
		t.Errorf("service: got %q, want %q", view.Service, "Product Service")
	}
	// The shopper should be told the shop is unavailable, not
	// shown a transport-level message.
	if strings.Contains(view.Error, "connection refused") {
		t.Errorf("message %q leaks the transport error", view.Error)
	}
	if !strings.Contains(view.Error, "unavailable") {
		t.Errorf("message %q does not explain that the service is unavailable", view.Error)
	}
}

func TestGetProductReturns504WhenTheProductServiceIsTooSlow(t *testing.T) {
	kit := newGatewayKit()

	// A dependency slower than the gateway's own budget.
	kit.gateway.requestLimit = 40 * time.Millisecond
	kit.product.delay = 2 * time.Second
	kit.handler = kit.gateway.Routes()

	start := time.Now()
	response := kit.do(http.MethodGet, "/api/products/P001", "")
	elapsed := time.Since(start)

	if response.Code != http.StatusGatewayTimeout {
		t.Fatalf("status: got %d, want 504 (%s)", response.Code, response.Body.String())
	}
	if elapsed > time.Second {
		t.Errorf("took %s to give up on a 40ms budget", elapsed)
	}

	view := decodeError(t, response)
	if !strings.Contains(view.Error, "too long") {
		t.Errorf("message %q does not explain that the request timed out", view.Error)
	}
}

func TestProductIsStillReturnedWhenInventoryIsDown(t *testing.T) {
	kit := newGatewayKit()
	kit.inventory.err = status.Error(codes.Unavailable, "connection refused")

	response := kit.do(http.MethodGet, "/api/products/P001", "")

	// Stock is supplementary, so losing Inventory must degrade
	// the answer rather than fail it.
	if response.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 (%s)", response.Code, response.Body.String())
	}

	var product productView
	_ = json.Unmarshal(response.Body.Bytes(), &product)

	if product.Name != "Laptop" {
		t.Errorf("name: got %q, want Laptop", product.Name)
	}
	if product.Stock != nil {
		t.Errorf("stock: got %v, want no value when Inventory is unreachable", *product.Stock)
	}
	if product.StockError == "" {
		t.Error("stock_error is empty; the UI needs a reason to show instead of a level")
	}
}

// ==========================================================
// Catalogue
// ==========================================================

func TestListProductsReturnsTheCatalogue(t *testing.T) {
	kit := newGatewayKit()

	response := kit.do(http.MethodGet, "/api/products", "")

	if response.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", response.Code)
	}

	var products []productView
	if err := json.Unmarshal(response.Body.Bytes(), &products); err != nil {
		t.Fatalf("could not decode the response: %v", err)
	}

	if len(products) != 3 {
		t.Fatalf("got %d products, want 3", len(products))
	}
	if products[0].ProductID != "P001" {
		t.Errorf("first product: got %q, want P001", products[0].ProductID)
	}
	if products[0].Stock == nil || *products[0].Stock != 10 {
		t.Error("catalogue entries should carry their stock level")
	}
}

func TestListProductsFiltersByCategory(t *testing.T) {
	kit := newGatewayKit()

	response := kit.do(http.MethodGet, "/api/products?category=accessories", "")

	if response.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", response.Code)
	}

	var products []productView
	_ = json.Unmarshal(response.Body.Bytes(), &products)

	if len(products) != 2 {
		t.Fatalf("got %d products, want 2", len(products))
	}
	for _, product := range products {
		if product.Category != "accessories" {
			t.Errorf("%s is in category %q, want accessories", product.ProductID, product.Category)
		}
	}
}

func TestListProductsReturnsAnEmptyArrayNotNull(t *testing.T) {
	kit := newGatewayKit()

	response := kit.do(http.MethodGet, "/api/products?category=furniture", "")

	if response.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", response.Code)
	}

	// `null` would make the UI's `.length` check throw; an
	// empty array renders as "no matching products".
	if body := strings.TrimSpace(response.Body.String()); body != "[]" {
		t.Errorf("body: got %q, want %q", body, "[]")
	}
}

func TestListProductsReports503WhenTheCatalogueIsUnreachable(t *testing.T) {
	kit := newGatewayKit()
	kit.product.listErr = status.Error(codes.Unavailable, "connection refused")

	response := kit.do(http.MethodGet, "/api/products", "")

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status: got %d, want 503", response.Code)
	}
}

func TestListProductsReportsAFailurePartWayThroughTheStream(t *testing.T) {
	kit := newGatewayKit()

	// The stream opens, then breaks. The gateway must report
	// the failure rather than serve a silently truncated
	// catalogue.
	kit.product.streamErr = status.Error(codes.Unavailable, "stream broke")

	response := kit.do(http.MethodGet, "/api/products", "")

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status: got %d, want 503 (%s)", response.Code, response.Body.String())
	}
}

// ==========================================================
// Status code mapping
// ==========================================================

func TestGRPCCodesMapToTheRightHTTPStatus(t *testing.T) {
	cases := []struct {
		code       codes.Code
		wantStatus int
	}{
		{codes.OK, http.StatusOK},
		{codes.InvalidArgument, http.StatusBadRequest},
		{codes.Unauthenticated, http.StatusUnauthorized},
		{codes.PermissionDenied, http.StatusForbidden},
		{codes.NotFound, http.StatusNotFound},
		{codes.AlreadyExists, http.StatusConflict},
		{codes.FailedPrecondition, http.StatusConflict},
		{codes.ResourceExhausted, http.StatusConflict},
		{codes.Aborted, http.StatusConflict},
		{codes.Unavailable, http.StatusServiceUnavailable},
		{codes.DeadlineExceeded, http.StatusGatewayTimeout},
		{codes.Unimplemented, http.StatusNotImplemented},
		{codes.Internal, http.StatusInternalServerError},
		{codes.Unknown, http.StatusInternalServerError},
	}

	for _, testCase := range cases {
		t.Run(testCase.code.String(), func(t *testing.T) {
			if got := httpStatusFor(testCase.code); got != testCase.wantStatus {
				t.Errorf("httpStatusFor(%s): got %d, want %d",
					testCase.code, got, testCase.wantStatus)
			}
		})
	}
}

func TestFriendlyMessageExplainsInfrastructureFailuresWithoutLeakingDetail(t *testing.T) {
	cases := []struct {
		name        string
		err         error
		wantContain string
		wantAbsent  string
	}{
		{
			name:        "unavailable",
			err:         status.Error(codes.Unavailable, "dial tcp 127.0.0.1:50051: connect: connection refused"),
			wantContain: "unavailable",
			wantAbsent:  "127.0.0.1",
		},
		{
			name:        "deadline exceeded",
			err:         status.Error(codes.DeadlineExceeded, "context deadline exceeded"),
			wantContain: "too long",
			wantAbsent:  "context",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			message := friendlyMessage("Product Service", testCase.err)

			if !strings.Contains(message, testCase.wantContain) {
				t.Errorf("message %q does not contain %q", message, testCase.wantContain)
			}
			if strings.Contains(message, testCase.wantAbsent) {
				t.Errorf("message %q leaks internal detail %q", message, testCase.wantAbsent)
			}
			if !strings.Contains(message, "Product Service") {
				t.Errorf("message %q does not name the service at fault", message)
			}
		})
	}
}

func TestFriendlyMessageKeepsServiceWordingForRequestErrors(t *testing.T) {
	// A service's own wording for a bad request is already
	// specific, so it is kept rather than replaced.
	message := friendlyMessage("Product Service",
		status.Error(codes.NotFound, "product with ID P999 not found"))

	if !strings.Contains(message, "P999") {
		t.Errorf("message %q dropped the detail from the service", message)
	}
	if !strings.HasPrefix(message, "Product with") {
		t.Errorf("message %q was not capitalised for display", message)
	}
	if !strings.HasSuffix(message, ".") {
		t.Errorf("message %q does not end as a sentence", message)
	}
}

func TestCapitalise(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"product not found", "Product not found"},
		{"product not found.", "Product not found"},
		{"  spaced  ", "Spaced"},
		{"", ""},
		{"A", "A"},
	}

	for _, testCase := range cases {
		if got := capitalise(testCase.in); got != testCase.want {
			t.Errorf("capitalise(%q): got %q, want %q", testCase.in, got, testCase.want)
		}
	}
}

// ==========================================================
// Orders
// ==========================================================

func TestCreateOrderRequiresASession(t *testing.T) {
	kit := newGatewayKit()

	response := kit.do(http.MethodPost, "/api/orders", `{"product_id":"P002","quantity":1}`)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status: got %d, want 401", response.Code)
	}

	view := decodeError(t, response)
	if view.Code != "UNAUTHENTICATED" {
		t.Errorf("code: got %q, want UNAUTHENTICATED", view.Code)
	}

	// Nothing should have reached the Order Service.
	if len(kit.order.created) != 0 {
		t.Error("an unauthenticated request still placed an order")
	}
}

func TestCreateOrderUsesTheSessionsCustomerNotTheRequestBody(t *testing.T) {
	kit := newGatewayKit()
	cookie := kit.signIn(t)

	// The body names a different customer. It must be ignored:
	// the order belongs to whoever holds the session.
	response := kit.do(http.MethodPost, "/api/orders",
		`{"customer_id":"C999","product_id":"P002","quantity":2}`, cookie)

	if response.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201 (%s)", response.Code, response.Body.String())
	}

	if len(kit.order.created) != 1 {
		t.Fatalf("%d orders were placed, want 1", len(kit.order.created))
	}
	if placed := kit.order.created[0].GetCustomerId(); placed != "C001" {
		t.Errorf("order was placed for %q, want C001 — the request body overrode the session", placed)
	}
}

func TestCreateOrderRejectsInvalidBodies(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"not JSON", `not json at all`},
		{"no product", `{"quantity":1}`},
		{"blank product", `{"product_id":"   ","quantity":1}`},
		{"zero quantity", `{"product_id":"P002","quantity":0}`},
		{"negative quantity", `{"product_id":"P002","quantity":-2}`},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			kit := newGatewayKit()
			cookie := kit.signIn(t)

			response := kit.do(http.MethodPost, "/api/orders", testCase.body, cookie)

			if response.Code != http.StatusBadRequest {
				t.Fatalf("status: got %d, want 400 (%s)", response.Code, response.Body.String())
			}
			if len(kit.order.created) != 0 {
				t.Error("an invalid request still reached the Order Service")
			}
		})
	}
}

func TestCreateOrderMapsServiceFailures(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{"out of stock", status.Error(codes.ResourceExhausted, "insufficient stock for product P003"), http.StatusConflict},
		{"payment declined", status.Error(codes.FailedPrecondition, "payment declined"), http.StatusConflict},
		{"unknown product", status.Error(codes.NotFound, "product with ID P999 not found"), http.StatusNotFound},
		{"service down", status.Error(codes.Unavailable, "connection refused"), http.StatusServiceUnavailable},
		{"timed out", status.Error(codes.DeadlineExceeded, "deadline exceeded"), http.StatusGatewayTimeout},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			kit := newGatewayKit()
			cookie := kit.signIn(t)
			kit.order.createErr = testCase.err

			response := kit.do(http.MethodPost, "/api/orders",
				`{"product_id":"P002","quantity":1}`, cookie)

			if response.Code != testCase.wantStatus {
				t.Errorf("status: got %d, want %d (%s)",
					response.Code, testCase.wantStatus, response.Body.String())
			}
		})
	}
}

func TestListOrdersRequiresASession(t *testing.T) {
	kit := newGatewayKit()

	response := kit.do(http.MethodGet, "/api/orders", "")

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status: got %d, want 401", response.Code)
	}
}

func TestListOrdersReturnsOnlyTheSignedInCustomersOrders(t *testing.T) {
	kit := newGatewayKit()
	kit.order.ordersByCustomer = map[string][]*pb.Order{
		"C001": {
			{OrderId: "ORD0001", CustomerId: "C001", ProductId: "P002",
				ProductName: "Mechanical Keyboard", Quantity: 1, TotalPrice: 4500,
				Status: "CONFIRMED", PaymentId: "PAY0001"},
		},
		"C002": {
			{OrderId: "ORD0002", CustomerId: "C002", ProductId: "P001", Quantity: 1},
		},
	}

	cookie := kit.signIn(t)
	response := kit.do(http.MethodGet, "/api/orders", "", cookie)

	if response.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", response.Code)
	}

	var orders []orderView
	_ = json.Unmarshal(response.Body.Bytes(), &orders)

	if len(orders) != 1 {
		t.Fatalf("got %d orders, want 1", len(orders))
	}
	if orders[0].OrderID != "ORD0001" {
		t.Errorf("order ID: got %q, want ORD0001", orders[0].OrderID)
	}
	if orders[0].PaymentID != "PAY0001" {
		t.Errorf("payment reference: got %q, want PAY0001", orders[0].PaymentID)
	}
	if orders[0].ProductName != "Mechanical Keyboard" {
		t.Errorf("product name: got %q, want Mechanical Keyboard", orders[0].ProductName)
	}
}

func TestListOrdersReturnsAnEmptyArrayForANewCustomer(t *testing.T) {
	kit := newGatewayKit()
	cookie := kit.signIn(t)

	response := kit.do(http.MethodGet, "/api/orders", "", cookie)

	if response.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", response.Code)
	}
	if body := strings.TrimSpace(response.Body.String()); body != "[]" {
		t.Errorf("body: got %q, want %q", body, "[]")
	}
}

// ==========================================================
// Routing
// ==========================================================

func TestRemovedCustomerLookupRouteIsGone(t *testing.T) {
	kit := newGatewayKit()
	cookie := kit.signIn(t)

	// Looking up an arbitrary customer by ID was removed when
	// accounts were introduced; it must not come back.
	response := kit.do(http.MethodGet, "/api/customers/C001", "", cookie)

	if response.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want 404 — arbitrary customer lookup should not be routable",
			response.Code)
	}
}

func TestUIIsServedAtTheRoot(t *testing.T) {
	kit := newGatewayKit()

	response := kit.do(http.MethodGet, "/", "")

	if response.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", response.Code)
	}
	if !strings.Contains(response.Body.String(), "<!DOCTYPE html>") {
		t.Error("the root path did not serve the storefront page")
	}
}

func TestStylesheetAndScriptAreServed(t *testing.T) {
	kit := newGatewayKit()

	for _, path := range []string{"/css/styles.css", "/js/app.js"} {
		t.Run(path, func(t *testing.T) {
			response := kit.do(http.MethodGet, path, "")

			if response.Code != http.StatusOK {
				t.Errorf("status: got %d, want 200", response.Code)
			}
			if response.Body.Len() == 0 {
				t.Error("the asset was served empty")
			}
		})
	}
}

func TestErrorResponsesAreJSON(t *testing.T) {
	kit := newGatewayKit()

	response := kit.do(http.MethodGet, "/api/products/P999", "")

	if contentType := response.Header().Get("Content-Type"); contentType != "application/json" {
		t.Errorf("Content-Type: got %q, want application/json", contentType)
	}
}
