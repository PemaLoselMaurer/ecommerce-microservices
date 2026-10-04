package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	pb "ecommerce-microservices/proto"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// frontEnd is the HTTP surface the chain tests drive. It is a
// stand-in for gateway-service, which lives in a package main
// and so cannot be imported.
//
// It deliberately mirrors the gateway's translation rules —
// the same gRPC-code-to-HTTP-status mapping and the same
// message wording — because that mapping is part of what the
// chain tests are checking. Those rules are also covered
// directly by the gateway's own unit tests, so a change in one
// place that is not made in the other shows up as a failure
// here rather than passing quietly.
type frontEnd struct {
	products  pb.ProductServiceClient
	inventory pb.InventoryServiceClient
	orders    pb.OrderServiceClient
	customers pb.CustomerServiceClient
}

const frontEndTimeout = 8 * time.Second

func (f *frontEnd) routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/products", f.listProducts)
	mux.HandleFunc("GET /api/products/{id}", f.getProduct)
	mux.HandleFunc("GET /api/orders", f.listOrders)
	mux.HandleFunc("POST /api/orders", f.createOrder)

	return mux
}

// ---------- handlers ----------

func (f *frontEnd) getProduct(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSON(w, http.StatusBadRequest, errorBody{
			Code: "INVALID_ARGUMENT", Error: "Please enter a product ID.",
		})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), frontEndTimeout)
	defer cancel()

	product, err := f.products.GetProduct(ctx, &pb.GetProductRequest{ProductId: id})
	if err != nil {
		writeGRPCError(w, "Product Service", err)
		return
	}

	view := toView(product)
	f.attachStock(ctx, &view)

	writeJSON(w, http.StatusOK, view)
}

func (f *frontEnd) listProducts(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), frontEndTimeout)
	defer cancel()

	stream, err := f.products.ListProducts(ctx, &pb.ListProductsRequest{
		Category: r.URL.Query().Get("category"),
	})
	if err != nil {
		writeGRPCError(w, "Product Service", err)
		return
	}

	products := []productBody{}
	for {
		product, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				break
			}
			writeGRPCError(w, "Product Service", err)
			return
		}

		view := toView(product)
		f.attachStock(ctx, &view)
		products = append(products, view)
	}

	writeJSON(w, http.StatusOK, products)
}

func (f *frontEnd) createOrder(w http.ResponseWriter, r *http.Request) {
	var body struct {
		CustomerID string `json:"customer_id"`
		ProductID  string `json:"product_id"`
		Quantity   int32  `json:"quantity"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody{
			Code: "INVALID_ARGUMENT", Error: "The request body could not be read as JSON.",
		})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), frontEndTimeout)
	defer cancel()

	placed, err := f.orders.CreateOrder(ctx, &pb.CreateOrderRequest{
		CustomerId: body.CustomerID,
		ProductId:  body.ProductID,
		Quantity:   body.Quantity,
	})
	if err != nil {
		writeGRPCError(w, "Order Service", err)
		return
	}

	writeJSON(w, http.StatusCreated, orderBody{
		OrderID:     placed.GetOrderId(),
		CustomerID:  placed.GetCustomerId(),
		ProductID:   placed.GetProductId(),
		ProductName: placed.GetProductName(),
		Quantity:    placed.GetQuantity(),
		TotalPrice:  placed.GetTotalPrice(),
		Status:      placed.GetStatus(),
		PaymentID:   placed.GetPaymentId(),
	})
}

func (f *frontEnd) listOrders(w http.ResponseWriter, r *http.Request) {
	customerID := r.URL.Query().Get("customer_id")

	ctx, cancel := context.WithTimeout(r.Context(), frontEndTimeout)
	defer cancel()

	stream, err := f.orders.ListOrdersByCustomer(ctx, &pb.ListOrdersRequest{CustomerId: customerID})
	if err != nil {
		writeGRPCError(w, "Order Service", err)
		return
	}

	orders := []orderBody{}
	for {
		placed, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				break
			}
			writeGRPCError(w, "Order Service", err)
			return
		}

		orders = append(orders, orderBody{
			OrderID:     placed.GetOrderId(),
			CustomerID:  placed.GetCustomerId(),
			ProductID:   placed.GetProductId(),
			ProductName: placed.GetProductName(),
			Quantity:    placed.GetQuantity(),
			TotalPrice:  placed.GetTotalPrice(),
			Status:      placed.GetStatus(),
			PaymentID:   placed.GetPaymentId(),
		})
	}

	writeJSON(w, http.StatusOK, orders)
}

// attachStock merges in the level from the Inventory Service,
// degrading rather than failing when it cannot be reached.
func (f *frontEnd) attachStock(ctx context.Context, view *productBody) {
	item, err := f.inventory.GetStock(ctx, &pb.GetStockRequest{ProductId: view.ProductID})
	if err != nil {
		view.StockError = friendlyMessage("Inventory Service", err)
		return
	}

	quantity := item.GetQuantity()
	view.Stock = &quantity
}

// ---------- payloads ----------

type productBody struct {
	ProductID  string  `json:"product_id"`
	Name       string  `json:"name"`
	Price      float64 `json:"price"`
	Category   string  `json:"category,omitempty"`
	Stock      *int32  `json:"stock,omitempty"`
	StockError string  `json:"stock_error,omitempty"`
}

type orderBody struct {
	OrderID     string  `json:"order_id"`
	CustomerID  string  `json:"customer_id"`
	ProductID   string  `json:"product_id"`
	ProductName string  `json:"product_name,omitempty"`
	Quantity    int32   `json:"quantity"`
	TotalPrice  float64 `json:"total_price"`
	Status      string  `json:"status"`
	PaymentID   string  `json:"payment_id,omitempty"`
}

type errorBody struct {
	Error   string `json:"error"`
	Code    string `json:"code"`
	Service string `json:"service,omitempty"`
}

func toView(product *pb.Product) productBody {
	return productBody{
		ProductID: product.GetProductId(),
		Name:      product.GetName(),
		Price:     product.GetPrice(),
		Category:  product.GetCategory(),
	}
}

// ---------- translation, matching the gateway ----------

func httpStatusFor(code codes.Code) int {
	switch code {
	case codes.OK:
		return http.StatusOK
	case codes.InvalidArgument:
		return http.StatusBadRequest
	case codes.Unauthenticated:
		return http.StatusUnauthorized
	case codes.PermissionDenied:
		return http.StatusForbidden
	case codes.NotFound:
		return http.StatusNotFound
	case codes.FailedPrecondition, codes.ResourceExhausted, codes.Aborted, codes.AlreadyExists:
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

func friendlyMessage(service string, err error) string {
	st, _ := status.FromError(err)

	switch st.Code() {
	case codes.Unavailable:
		return fmt.Sprintf("The %s is unavailable right now. Please try again shortly.", service)
	case codes.DeadlineExceeded:
		return fmt.Sprintf("The %s took too long to respond and the request timed out.", service)
	default:
		return capitalise(st.Message()) + "."
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
	writeJSON(w, httpStatusFor(st.Code()), errorBody{
		Error:   friendlyMessage(service, err),
		Code:    st.Code().String(),
		Service: service,
	})
}

func writeJSON(w http.ResponseWriter, httpStatus int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(httpStatus)
	_ = json.NewEncoder(w).Encode(payload)
}
