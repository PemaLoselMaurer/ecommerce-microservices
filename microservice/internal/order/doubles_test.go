package order

import (
	"context"
	"fmt"
	"sync"

	"ecommerce-microservices/internal/pricing"
	pb "ecommerce-microservices/proto"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Test doubles for the five services the Order Service depends
// on. Each implements the generated client interface, so the
// Order Service under test cannot tell them from the real
// thing — but every response, error and delay is scripted,
// which lets these tests reproduce failures that would be
// impractical to trigger against live services.

// ---------- Customer ----------

type fakeCustomerClient struct {
	customers map[string]*pb.Customer
	err       error // when set, every call fails with it
	calls     int
}

func (f *fakeCustomerClient) GetCustomer(
	ctx context.Context, in *pb.GetCustomerRequest, opts ...grpc.CallOption,
) (*pb.Customer, error) {
	f.calls++

	if f.err != nil {
		return nil, f.err
	}

	customer, exists := f.customers[in.GetCustomerId()]
	if !exists {
		return nil, status.Errorf(codes.NotFound, "customer with ID %s not found", in.GetCustomerId())
	}
	return customer, nil
}

func (f *fakeCustomerClient) CreateCustomer(
	ctx context.Context, in *pb.CreateCustomerRequest, opts ...grpc.CallOption,
) (*pb.Customer, error) {
	return nil, status.Error(codes.Unimplemented, "not used by the Order Service")
}

func (f *fakeCustomerClient) Authenticate(
	ctx context.Context, in *pb.AuthenticateRequest, opts ...grpc.CallOption,
) (*pb.Customer, error) {
	return nil, status.Error(codes.Unimplemented, "not used by the Order Service")
}

// ---------- Product ----------

type fakeProductClient struct {
	products map[string]*pb.Product
	err      error
	calls    int
}

func (f *fakeProductClient) GetProduct(
	ctx context.Context, in *pb.GetProductRequest, opts ...grpc.CallOption,
) (*pb.Product, error) {
	f.calls++

	if f.err != nil {
		return nil, f.err
	}

	product, exists := f.products[in.GetProductId()]
	if !exists {
		return nil, status.Errorf(codes.NotFound, "product with ID %s not found", in.GetProductId())
	}
	return product, nil
}

func (f *fakeProductClient) ListProducts(
	ctx context.Context, in *pb.ListProductsRequest, opts ...grpc.CallOption,
) (grpc.ServerStreamingClient[pb.Product], error) {
	return nil, status.Error(codes.Unimplemented, "not used by the Order Service")
}

// ---------- Inventory ----------

type fakeInventoryClient struct {
	stock map[string]int32
	err   error
	calls int

	// reserved records what was actually taken, so a test can
	// prove stock was or was not touched.
	reserved []*pb.ReserveStockRequest
}

func (f *fakeInventoryClient) ReserveStock(
	ctx context.Context, in *pb.ReserveStockRequest, opts ...grpc.CallOption,
) (*pb.StockItem, error) {
	f.calls++

	if f.err != nil {
		return nil, f.err
	}

	quantity, exists := f.stock[in.GetProductId()]
	if !exists {
		return nil, status.Errorf(codes.NotFound, "no stock record for product %s", in.GetProductId())
	}
	if quantity < in.GetQuantity() {
		return nil, status.Errorf(codes.ResourceExhausted,
			"insufficient stock for product %s: have %d, requested %d",
			in.GetProductId(), quantity, in.GetQuantity())
	}

	f.stock[in.GetProductId()] = quantity - in.GetQuantity()
	f.reserved = append(f.reserved, in)

	return &pb.StockItem{ProductId: in.GetProductId(), Quantity: f.stock[in.GetProductId()]}, nil
}

func (f *fakeInventoryClient) GetStock(
	ctx context.Context, in *pb.GetStockRequest, opts ...grpc.CallOption,
) (*pb.StockItem, error) {
	quantity, exists := f.stock[in.GetProductId()]
	if !exists {
		return nil, status.Errorf(codes.NotFound, "no stock record for product %s", in.GetProductId())
	}
	return &pb.StockItem{ProductId: in.GetProductId(), Quantity: quantity}, nil
}

func (f *fakeInventoryClient) WatchStock(
	ctx context.Context, in *pb.WatchStockRequest, opts ...grpc.CallOption,
) (grpc.ServerStreamingClient[pb.StockItem], error) {
	return nil, status.Error(codes.Unimplemented, "not used by the Order Service")
}

func (f *fakeInventoryClient) SyncStock(
	ctx context.Context, opts ...grpc.CallOption,
) (grpc.BidiStreamingClient[pb.StockAdjustment, pb.StockAlert], error) {
	return nil, status.Error(codes.Unimplemented, "not used by the Order Service")
}

// ---------- Payment ----------

type fakePaymentClient struct {
	err      error
	calls    int
	charged  []float64
	nextID   int
	declines bool // decline every charge as insufficient funds
}

func (f *fakePaymentClient) ProcessPayment(
	ctx context.Context, in *pb.PaymentRequest, opts ...grpc.CallOption,
) (*pb.PaymentResult, error) {
	f.calls++

	if f.err != nil {
		return nil, f.err
	}
	if f.declines {
		return nil, status.Errorf(codes.FailedPrecondition,
			"payment declined: insufficient funds for amount %.2f", in.GetAmount())
	}

	f.charged = append(f.charged, in.GetAmount())
	f.nextID++

	return &pb.PaymentResult{
		PaymentId: fmt.Sprintf("PAY%04d", f.nextID),
		Status:    "SUCCESS",
		Message:   "approved",
	}, nil
}

// ---------- Notification ----------

type fakeNotificationClient struct {
	mu    sync.Mutex
	err   error
	calls int
	sent  []*pb.Notification
}

func (f *fakeNotificationClient) Notify(
	ctx context.Context, in *pb.Notification, opts ...grpc.CallOption,
) (*pb.NotifyResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls++

	if f.err != nil {
		return nil, f.err
	}

	f.sent = append(f.sent, in)
	return &pb.NotifyResponse{Delivered: true}, nil
}

func (f *fakeNotificationClient) SubscribeNotifications(
	ctx context.Context, in *pb.SubscribeRequest, opts ...grpc.CallOption,
) (grpc.ServerStreamingClient[pb.Notification], error) {
	return nil, status.Error(codes.Unimplemented, "not used by the Order Service")
}

// ---------- Pricing (the serverless function) ----------

// fakePricingClient stands in for the calculate-order-price
// Worker. By default it prices flat (unit price × quantity);
// quote, when set, scripts a different breakdown.
type fakePricingClient struct {
	err      error
	quote    func(pricing.Request) *pricing.Quote
	calls    int
	requests []pricing.Request
}

func (f *fakePricingClient) Price(ctx context.Context, req pricing.Request) (*pricing.Quote, error) {
	f.calls++
	f.requests = append(f.requests, req)

	if f.err != nil {
		return nil, f.err
	}
	if f.quote != nil {
		return f.quote(req), nil
	}
	return pricing.Flat{}.Price(ctx, req)
}

// ---------- stream double ----------

// recordingOrderStream captures orders sent by
// ListOrdersByCustomer.
type recordingOrderStream struct {
	grpc.ServerStream
	sent []*pb.Order
	err  error
}

func (s *recordingOrderStream) Send(order *pb.Order) error {
	if s.err != nil {
		return s.err
	}
	s.sent = append(s.sent, order)
	return nil
}

func (s *recordingOrderStream) Context() context.Context { return context.Background() }
