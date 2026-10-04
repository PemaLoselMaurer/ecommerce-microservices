package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	pb "ecommerce-microservices/proto"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Gateway is the HTTP front end for the e-commerce
// microservices. It owns no data of its own: every response it
// serves is obtained by calling one of the gRPC services, so
// the browser UI never touches a service's internal store
// directly.
//
// Its dependencies are the generated gRPC client interfaces
// rather than concrete connections, which is what lets the
// unit tests in Part B swap in test doubles and run without
// any service listening.
type Gateway struct {
	products     pb.ProductServiceClient
	customers    pb.CustomerServiceClient
	inventory    pb.InventoryServiceClient
	orders       pb.OrderServiceClient
	requestLimit time.Duration

	// sessions records who is signed in. Any handler that acts
	// on a customer's own data reads the customer ID from here
	// rather than from the request, so a shopper can only ever
	// reach their own orders and account.
	sessions *SessionStore

	// Health, when set, is served at GET /api/health so the UI
	// can show which services it can currently reach. It is
	// wired up in main.go, where the underlying connections
	// are available.
	Health http.HandlerFunc
}

// NewGateway builds a Gateway over the given service clients.
// requestLimit bounds how long any single upstream call may
// take before the gateway gives up on it.
func NewGateway(
	products pb.ProductServiceClient,
	customers pb.CustomerServiceClient,
	inventory pb.InventoryServiceClient,
	orders pb.OrderServiceClient,
	requestLimit time.Duration,
) *Gateway {
	return &Gateway{
		products:     products,
		customers:    customers,
		inventory:    inventory,
		orders:       orders,
		requestLimit: requestLimit,
		sessions:     NewSessionStore(),
	}
}

// ---------- response payloads ----------

type productView struct {
	ProductID   string  `json:"product_id"`
	Name        string  `json:"name"`
	Price       float64 `json:"price"`
	ListPrice   float64 `json:"list_price,omitempty"`
	Badge       string  `json:"badge,omitempty"`
	Rating      float64 `json:"rating,omitempty"`
	ReviewCount int32   `json:"review_count,omitempty"`
	Category    string  `json:"category,omitempty"`
	Description string  `json:"description,omitempty"`
	// Stock is filled in from the Inventory Service. If
	// Inventory cannot be reached the product is still
	// returned, with StockError explaining why the level is
	// missing — a partial answer beats no answer.
	Stock      *int32 `json:"stock,omitempty"`
	StockError string `json:"stock_error,omitempty"`
}

type customerView struct {
	CustomerID string `json:"customer_id"`
	Name       string `json:"name"`
	Email      string `json:"email"`
	Address    string `json:"address"`
}

type orderView struct {
	OrderID     string  `json:"order_id"`
	CustomerID  string  `json:"customer_id"`
	ProductID   string  `json:"product_id"`
	ProductName string  `json:"product_name,omitempty"`
	Quantity    int32   `json:"quantity"`
	TotalPrice  float64 `json:"total_price"`
	Status      string  `json:"status"`
	PaymentID   string  `json:"payment_id,omitempty"`

	// The breakdown from the serverless pricing function.
	Subtotal     float64  `json:"subtotal"`
	Discount     float64  `json:"discount"`
	DeliveryFee  float64  `json:"delivery_fee"`
	PricingRules []string `json:"pricing_rules"`
}

// quoteView is a price, from the serverless pricing function,
// for an order that has not been placed.
type quoteView struct {
	ProductID      string   `json:"product_id"`
	ProductName    string   `json:"product_name"`
	Quantity       int32    `json:"quantity"`
	UnitPrice      float64  `json:"unit_price"`
	Subtotal       float64  `json:"subtotal"`
	DiscountRate   float64  `json:"discount_rate"`
	Discount       float64  `json:"discount"`
	DeliveryFee    float64  `json:"delivery_fee"`
	Total          float64  `json:"total"`
	PricingRules   []string `json:"pricing_rules"`
	PricingVersion string   `json:"pricing_version"`
}

// createOrderBody is the checkout payload. It deliberately has
// no customer field — who is ordering comes from the session.
type createOrderBody struct {
	ProductID string `json:"product_id"`
	Quantity  int32  `json:"quantity"`
}

// errorView is the single error shape the UI understands.
// Code is the machine-readable reason, Error the sentence
// shown to the user.
type errorView struct {
	Error   string `json:"error"`
	Code    string `json:"code"`
	Service string `json:"service,omitempty"`
}

// ---------- routing ----------

// Routes returns the gateway's HTTP handler, including the
// embedded single-page UI served at "/".
func (g *Gateway) Routes() http.Handler {
	mux := http.NewServeMux()

	// Browsing the catalogue needs no account.
	mux.HandleFunc("GET /api/products", g.handleListProducts)
	mux.HandleFunc("GET /api/products/{id}", g.handleGetProduct)

	// A price quote reads no customer data, so it needs no
	// account either.
	mux.HandleFunc("POST /api/orders/quote", g.handleQuoteOrder)

	// Authentication.
	mux.HandleFunc("POST /api/auth/register", g.handleRegister)
	mux.HandleFunc("POST /api/auth/login", g.handleLogin)
	mux.HandleFunc("POST /api/auth/logout", g.handleLogout)
	mux.HandleFunc("GET /api/auth/me", g.handleMe)

	// Everything below acts on the signed-in customer's own
	// data. The customer ID comes from the session, never from
	// the URL or request body, so one shopper cannot read or
	// order on behalf of another.
	mux.HandleFunc("GET /api/orders", g.requireSession(g.handleListOrders))
	mux.HandleFunc("POST /api/orders", g.requireSession(g.handleCreateOrder))

	// Requests for a bare collection carry no identifier, so
	// they are answered with the same "identifier required"
	// error the UI shows for an empty input box.
	mux.HandleFunc("GET /api/products/", g.handleMissingID)

	if g.Health != nil {
		mux.HandleFunc("GET /api/health", g.Health)
	}

	mux.Handle("GET /", http.FileServerFS(uiFiles))

	return mux
}

// ---------- handlers ----------

func (g *Gateway) handleGetProduct(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, errorView{
			Code:  "INVALID_ARGUMENT",
			Error: "Please enter a product ID.",
		})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), g.requestLimit)
	defer cancel()

	product, err := g.products.GetProduct(ctx, &pb.GetProductRequest{ProductId: id})
	if err != nil {
		writeGRPCError(w, "Product Service", err)
		return
	}

	view := toProductView(product)
	g.attachStock(ctx, &view)

	writeJSON(w, http.StatusOK, view)
}

// toProductView converts a catalogue entry into the shape the
// storefront consumes. Stock is added separately, since it
// comes from a different service.
func toProductView(product *pb.Product) productView {
	return productView{
		ProductID:   product.GetProductId(),
		Name:        product.GetName(),
		Price:       product.GetPrice(),
		ListPrice:   product.GetListPrice(),
		Badge:       product.GetBadge(),
		Rating:      product.GetRating(),
		ReviewCount: product.GetReviewCount(),
		Category:    product.GetCategory(),
		Description: product.GetDescription(),
	}
}

// handleListProducts serves the storefront's product grid. The
// catalogue itself comes from the Product Service; each entry
// is then enriched with a live stock level from Inventory.
func (g *Gateway) handleListProducts(w http.ResponseWriter, r *http.Request) {
	category := strings.TrimSpace(r.URL.Query().Get("category"))

	// Listing is a fan-out over the whole catalogue, so it gets
	// a longer budget than a single lookup.
	ctx, cancel := context.WithTimeout(r.Context(), g.requestLimit*2)
	defer cancel()

	stream, err := g.products.ListProducts(ctx, &pb.ListProductsRequest{Category: category})
	if err != nil {
		writeGRPCError(w, "Product Service", err)
		return
	}

	products := []productView{}
	for {
		product, err := stream.Recv()
		if err != nil {
			if isStreamEnd(err) {
				break
			}
			writeGRPCError(w, "Product Service", err)
			return
		}

		view := toProductView(product)
		g.attachStock(ctx, &view)
		products = append(products, view)
	}

	writeJSON(w, http.StatusOK, products)
}

// attachStock fills in a product's stock level from the
// Inventory Service. Stock is supplementary detail, so an
// Inventory failure is recorded on the view rather than
// failing the whole response — the storefront still shows the
// product, with its availability marked unknown.
func (g *Gateway) attachStock(ctx context.Context, view *productView) {
	stock, err := g.inventory.GetStock(ctx, &pb.GetStockRequest{ProductId: view.ProductID})
	if err != nil {
		view.StockError = friendlyMessage("Inventory Service", err)
		return
	}

	quantity := stock.GetQuantity()
	view.Stock = &quantity
}

// handleListOrders returns the signed-in customer's orders.
// customerID is supplied by requireSession, not by the request.
func (g *Gateway) handleListOrders(w http.ResponseWriter, r *http.Request, customerID string) {
	ctx, cancel := context.WithTimeout(r.Context(), g.requestLimit)
	defer cancel()

	stream, err := g.orders.ListOrdersByCustomer(ctx, &pb.ListOrdersRequest{CustomerId: customerID})
	if err != nil {
		writeGRPCError(w, "Order Service", err)
		return
	}

	orders := []orderView{}
	for {
		order, err := stream.Recv()
		if err != nil {
			if isStreamEnd(err) {
				break
			}
			writeGRPCError(w, "Order Service", err)
			return
		}
		orders = append(orders, toOrderView(order))
	}

	writeJSON(w, http.StatusOK, orders)
}

// handleCreateOrder places an order for the signed-in
// customer. The account charged is whoever holds the session,
// so the request body carries only what is being bought.
func (g *Gateway) handleCreateOrder(w http.ResponseWriter, r *http.Request, customerID string) {
	body, ok := readOrderBody(w, r)
	if !ok {
		return
	}

	// Placing an order fans out to five services behind the
	// Order Service, so it gets a longer budget than a single
	// lookup.
	ctx, cancel := context.WithTimeout(r.Context(), g.requestLimit*2)
	defer cancel()

	order, err := g.orders.CreateOrder(ctx, &pb.CreateOrderRequest{
		CustomerId: customerID,
		ProductId:  body.ProductID,
		Quantity:   body.Quantity,
	})
	if err != nil {
		writeGRPCError(w, "Order Service", err)
		return
	}

	writeJSON(w, http.StatusCreated, toOrderView(order))
}

// handleQuoteOrder asks the Order Service what an order would
// cost. The Order Service gets the product from the Product
// Service and the price from the serverless pricing function;
// the gateway only relays the result.
func (g *Gateway) handleQuoteOrder(w http.ResponseWriter, r *http.Request) {
	body, ok := readOrderBody(w, r)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), g.requestLimit*2)
	defer cancel()

	quote, err := g.orders.QuoteOrder(ctx, &pb.QuoteOrderRequest{
		ProductId: body.ProductID,
		Quantity:  body.Quantity,
	})
	if err != nil {
		writeGRPCError(w, "Order Service", err)
		return
	}

	writeJSON(w, http.StatusOK, quoteView{
		ProductID:      quote.GetProductId(),
		ProductName:    quote.GetProductName(),
		Quantity:       quote.GetQuantity(),
		UnitPrice:      quote.GetUnitPrice(),
		Subtotal:       quote.GetSubtotal(),
		DiscountRate:   quote.GetDiscountRate(),
		Discount:       quote.GetDiscount(),
		DeliveryFee:    quote.GetDeliveryFee(),
		Total:          quote.GetTotal(),
		PricingRules:   nonNil(quote.GetPricingRules()),
		PricingVersion: quote.GetPricingVersion(),
	})
}

// readOrderBody decodes and checks a {product_id, quantity}
// body, answering the request itself when it is unusable.
func readOrderBody(w http.ResponseWriter, r *http.Request) (createOrderBody, bool) {
	var body createOrderBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, errorView{
			Code:  "INVALID_ARGUMENT",
			Error: "The request body could not be read as JSON.",
		})
		return body, false
	}

	body.ProductID = strings.TrimSpace(body.ProductID)

	if body.ProductID == "" {
		writeError(w, http.StatusBadRequest, errorView{
			Code:  "INVALID_ARGUMENT",
			Error: "Please choose a product to order.",
		})
		return body, false
	}
	if body.Quantity <= 0 {
		writeError(w, http.StatusBadRequest, errorView{
			Code:  "INVALID_ARGUMENT",
			Error: "Quantity must be a whole number greater than zero.",
		})
		return body, false
	}
	return body, true
}

// nonNil keeps an empty list as [] in JSON rather than null.
func nonNil(items []string) []string {
	if items == nil {
		return []string{}
	}
	return items
}

func (g *Gateway) handleMissingID(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusBadRequest, errorView{
		Code:  "INVALID_ARGUMENT",
		Error: "Please enter an ID.",
	})
}

// ---------- helpers ----------

func toOrderView(order *pb.Order) orderView {
	return orderView{
		OrderID:     order.GetOrderId(),
		CustomerID:  order.GetCustomerId(),
		ProductID:   order.GetProductId(),
		ProductName: order.GetProductName(),
		Quantity:    order.GetQuantity(),
		TotalPrice:  order.GetTotalPrice(),
		Status:      order.GetStatus(),
		PaymentID:   order.GetPaymentId(),

		Subtotal:     order.GetSubtotal(),
		Discount:     order.GetDiscount(),
		DeliveryFee:  order.GetDeliveryFee(),
		PricingRules: nonNil(order.GetPricingRules()),
	}
}

// isStreamEnd reports whether err marks the normal end of a
// server stream rather than a failure.
func isStreamEnd(err error) bool {
	if err == nil {
		return false
	}
	if err.Error() == "EOF" {
		return true
	}
	st, ok := status.FromError(err)
	return ok && st.Code() == codes.OK
}

// httpStatusFor translates a gRPC status code into the HTTP
// status the UI should see.
func httpStatusFor(code codes.Code) int {
	switch code {
	case codes.OK:
		return http.StatusOK
	case codes.InvalidArgument:
		return http.StatusBadRequest
	case codes.NotFound:
		return http.StatusNotFound
	case codes.FailedPrecondition, codes.ResourceExhausted, codes.Aborted:
		return http.StatusConflict
	case codes.Unauthenticated:
		return http.StatusUnauthorized
	case codes.PermissionDenied:
		return http.StatusForbidden
	case codes.AlreadyExists:
		return http.StatusConflict
	case codes.Unavailable:
		return http.StatusServiceUnavailable
	case codes.DeadlineExceeded:
		return http.StatusGatewayTimeout
	case codes.Unimplemented:
		return http.StatusNotImplemented
	default:
		return http.StatusInternalServerError
	}
}

// friendlyMessage turns a gRPC error into a sentence suitable
// for display in the UI. Codes that describe an infrastructure
// problem get a generic explanation naming the service at
// fault; codes that describe the request itself keep the
// service's own wording, which is already specific.
func friendlyMessage(service string, err error) string {
	st, _ := status.FromError(err)

	// The Order Service reports pricing-function outages with a
	// message starting "pricing service". The Order Service
	// itself is fine in that case, so the customer is told what
	// actually failed, still without the Worker's address.
	pricingFault := strings.HasPrefix(st.Message(), "pricing service")

	switch st.Code() {
	case codes.Unavailable:
		if pricingFault {
			return "The pricing service is unavailable right now, so your order could not be priced. Please try again shortly."
		}
		return fmt.Sprintf(
			"The %s is unavailable right now. Please try again shortly.", service,
		)
	case codes.DeadlineExceeded:
		if pricingFault {
			return "The pricing service took too long to respond, so your order could not be priced. Please try again."
		}
		return fmt.Sprintf(
			"The %s took too long to respond and the request timed out.", service,
		)
	case codes.NotFound, codes.InvalidArgument, codes.FailedPrecondition,
		codes.ResourceExhausted, codes.Unauthenticated, codes.AlreadyExists,
		codes.PermissionDenied:
		return capitalise(st.Message()) + "."
	default:
		return fmt.Sprintf("The %s returned an unexpected error: %s.", service, st.Message())
	}
}

func capitalise(message string) string {
	message = strings.TrimSuffix(strings.TrimSpace(message), ".")
	if message == "" {
		return message
	}
	return strings.ToUpper(message[:1]) + message[1:]
}

func writeGRPCError(w http.ResponseWriter, service string, err error) {
	st, _ := status.FromError(err)
	writeError(w, httpStatusFor(st.Code()), errorView{
		Error:   friendlyMessage(service, err),
		Code:    st.Code().String(),
		Service: service,
	})
}

func writeError(w http.ResponseWriter, httpStatus int, view errorView) {
	writeJSON(w, httpStatus, view)
}

func writeJSON(w http.ResponseWriter, httpStatus int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(httpStatus)
	_ = json.NewEncoder(w).Encode(payload)
}
