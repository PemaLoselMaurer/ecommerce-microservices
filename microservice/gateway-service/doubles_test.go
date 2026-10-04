package main

import (
	"context"
	"fmt"
	"io"
	"time"

	pb "ecommerce-microservices/proto"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// Test doubles for the four services the gateway calls. They
// implement the generated client interfaces, so the gateway
// under test behaves exactly as it would against real
// services, while every response and failure stays scripted.

// ---------- a stream double ----------

// fakeStream replays a fixed slice of messages and then EOF,
// standing in for a gRPC server-streaming client.
type fakeStream[T any] struct {
	items []*T
	index int
	err   error // when set, Recv fails with it instead of returning an item
}

func (s *fakeStream[T]) Recv() (*T, error) {
	if s.err != nil {
		return nil, s.err
	}
	if s.index >= len(s.items) {
		return nil, io.EOF
	}

	item := s.items[s.index]
	s.index++
	return item, nil
}

// The remaining methods satisfy grpc.ClientStream; none of
// them are exercised by the gateway's read-only use of a
// stream.
func (s *fakeStream[T]) Header() (metadata.MD, error) { return nil, nil }
func (s *fakeStream[T]) Trailer() metadata.MD         { return nil }
func (s *fakeStream[T]) CloseSend() error             { return nil }
func (s *fakeStream[T]) Context() context.Context     { return context.Background() }
func (s *fakeStream[T]) SendMsg(m any) error          { return nil }
func (s *fakeStream[T]) RecvMsg(m any) error          { return nil }

// ---------- Product ----------

type fakeProductClient struct {
	products map[string]*pb.Product
	order    []string // catalogue order for ListProducts

	getErr    error
	listErr   error // failure opening the stream
	streamErr error // failure part-way through the stream
	delay     time.Duration
}

func (f *fakeProductClient) GetProduct(
	ctx context.Context, in *pb.GetProductRequest, opts ...grpc.CallOption,
) (*pb.Product, error) {
	if err := f.wait(ctx); err != nil {
		return nil, err
	}
	if f.getErr != nil {
		return nil, f.getErr
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
	if err := f.wait(ctx); err != nil {
		return nil, err
	}
	if f.listErr != nil {
		return nil, f.listErr
	}

	items := make([]*pb.Product, 0, len(f.order))
	for _, id := range f.order {
		product := f.products[id]
		if in.GetCategory() != "" && product.GetCategory() != in.GetCategory() {
			continue
		}
		items = append(items, product)
	}

	return &fakeStream[pb.Product]{items: items, err: f.streamErr}, nil
}

// wait honours the caller's deadline, so a test can make a
// dependency look slow.
func (f *fakeProductClient) wait(ctx context.Context) error {
	if f.delay == 0 {
		return nil
	}
	select {
	case <-ctx.Done():
		return status.FromContextError(ctx.Err()).Err()
	case <-time.After(f.delay):
		return nil
	}
}

// ---------- Customer ----------

type fakeCustomerClient struct {
	customers map[string]*pb.Customer

	// credentials maps email to the customer ID it signs in as.
	credentials map[string]string
	password    string

	getErr    error
	createErr error
	authErr   error
	nextID    string
}

func (f *fakeCustomerClient) GetCustomer(
	ctx context.Context, in *pb.GetCustomerRequest, opts ...grpc.CallOption,
) (*pb.Customer, error) {
	if f.getErr != nil {
		return nil, f.getErr
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
	if f.createErr != nil {
		return nil, f.createErr
	}

	id := f.nextID
	if id == "" {
		id = "C003"
	}

	customer := &pb.Customer{
		CustomerId: id,
		Name:       in.GetName(),
		Email:      in.GetEmail(),
		Address:    in.GetAddress(),
	}
	f.customers[id] = customer
	f.credentials[in.GetEmail()] = id

	return customer, nil
}

func (f *fakeCustomerClient) Authenticate(
	ctx context.Context, in *pb.AuthenticateRequest, opts ...grpc.CallOption,
) (*pb.Customer, error) {
	if f.authErr != nil {
		return nil, f.authErr
	}

	customerID, known := f.credentials[in.GetEmail()]
	if !known || in.GetPassword() != f.password {
		return nil, status.Error(codes.Unauthenticated, "incorrect email or password")
	}
	return f.customers[customerID], nil
}

// ---------- Inventory ----------

type fakeInventoryClient struct {
	stock map[string]int32
	err   error
}

func (f *fakeInventoryClient) GetStock(
	ctx context.Context, in *pb.GetStockRequest, opts ...grpc.CallOption,
) (*pb.StockItem, error) {
	if f.err != nil {
		return nil, f.err
	}

	quantity, exists := f.stock[in.GetProductId()]
	if !exists {
		return nil, status.Errorf(codes.NotFound, "no stock record for product %s", in.GetProductId())
	}
	return &pb.StockItem{ProductId: in.GetProductId(), Quantity: quantity}, nil
}

func (f *fakeInventoryClient) ReserveStock(
	ctx context.Context, in *pb.ReserveStockRequest, opts ...grpc.CallOption,
) (*pb.StockItem, error) {
	return nil, status.Error(codes.Unimplemented, "not used by the gateway")
}

func (f *fakeInventoryClient) WatchStock(
	ctx context.Context, in *pb.WatchStockRequest, opts ...grpc.CallOption,
) (grpc.ServerStreamingClient[pb.StockItem], error) {
	return nil, status.Error(codes.Unimplemented, "not used by the gateway")
}

func (f *fakeInventoryClient) SyncStock(
	ctx context.Context, opts ...grpc.CallOption,
) (grpc.BidiStreamingClient[pb.StockAdjustment, pb.StockAlert], error) {
	return nil, status.Error(codes.Unimplemented, "not used by the gateway")
}

// ---------- Order ----------

type fakeOrderClient struct {
	// ordersByCustomer is what ListOrdersByCustomer replays.
	ordersByCustomer map[string][]*pb.Order

	createErr error
	listErr   error
	streamErr error
	delay     time.Duration

	// created records every CreateOrder request, so a test can
	// prove which customer an order was placed for.
	created []*pb.CreateOrderRequest
	nextID  int

	quoteErr error
	quoted   []*pb.QuoteOrderRequest
}

func (f *fakeOrderClient) CreateOrder(
	ctx context.Context, in *pb.CreateOrderRequest, opts ...grpc.CallOption,
) (*pb.Order, error) {
	if f.delay > 0 {
		select {
		case <-ctx.Done():
			return nil, status.FromContextError(ctx.Err()).Err()
		case <-time.After(f.delay):
		}
	}
	if f.createErr != nil {
		return nil, f.createErr
	}

	f.created = append(f.created, in)
	f.nextID++

	return &pb.Order{
		OrderId:    fmt.Sprintf("ORD%04d", f.nextID),
		CustomerId: in.GetCustomerId(),
		ProductId:  in.GetProductId(),
		Quantity:   in.GetQuantity(),
		TotalPrice: 1000 * float64(in.GetQuantity()),
		Status:     "CONFIRMED",
		PaymentId:  "PAY0001",
	}, nil
}

func (f *fakeOrderClient) ListOrdersByCustomer(
	ctx context.Context, in *pb.ListOrdersRequest, opts ...grpc.CallOption,
) (grpc.ServerStreamingClient[pb.Order], error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return &fakeStream[pb.Order]{items: f.ordersByCustomer[in.GetCustomerId()], err: f.streamErr}, nil
}

func (f *fakeOrderClient) QuoteOrder(
	ctx context.Context, in *pb.QuoteOrderRequest, opts ...grpc.CallOption,
) (*pb.OrderQuote, error) {
	if f.quoteErr != nil {
		return nil, f.quoteErr
	}

	f.quoted = append(f.quoted, in)

	// 1000 each, 5% off for 3 or more, Nu. 150 delivery below
	// Nu. 5,000 — enough shape for the gateway to pass through.
	subtotal := 1000 * float64(in.GetQuantity())
	discount := 0.0
	if in.GetQuantity() >= 3 {
		discount = subtotal * 0.05
	}
	delivery := 0.0
	if subtotal-discount < 5000 {
		delivery = 150
	}
	return &pb.OrderQuote{
		ProductId:      in.GetProductId(),
		ProductName:    "Product " + in.GetProductId(),
		Quantity:       in.GetQuantity(),
		UnitPrice:      1000,
		Subtotal:       subtotal,
		Discount:       discount,
		DeliveryFee:    delivery,
		Total:          subtotal - discount + delivery,
		PricingRules:   []string{"TEST_RULE"},
		PricingVersion: "test",
	}, nil
}
