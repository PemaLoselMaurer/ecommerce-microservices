// Package order holds the Order Service implementation. It is
// the only service that depends on every other one, so it is
// where the resilience patterns and the integration tests are
// most concentrated.
package order

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"ecommerce-microservices/internal/pricing"
	pb "ecommerce-microservices/proto"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Clients are the five services an order needs to be
// fulfilled, plus the serverless pricing function. They are
// interfaces, so the Order Service can be wired to real
// connections in production and to test doubles or in-process
// servers in tests.
type Clients struct {
	Customer     pb.CustomerServiceClient
	Product      pb.ProductServiceClient
	Inventory    pb.InventoryServiceClient
	Payment      pb.PaymentServiceClient
	Notification pb.NotificationServiceClient

	// Pricing is the calculate-order-price serverless function.
	// When nil, every order is refused rather than priced
	// without it.
	Pricing pricing.Client
}

// Server implements the OrderService defined in order.proto.
// It is a gRPC server for CreateOrder / ListOrdersByCustomer,
// and internally acts as a gRPC client to the Customer,
// Product, Inventory, Payment and Notification services to
// fulfil an order end to end.
type Server struct {
	pb.UnimplementedOrderServiceServer

	clients Clients

	// fanOutTimeout bounds the whole fan-out across the five
	// dependencies.
	fanOutTimeout time.Duration

	mu         sync.Mutex
	orders     map[string]*pb.Order
	byCustomer map[string][]string
	nextID     int
}

// DefaultFanOutTimeout is how long CreateOrder will spend on
// its dependencies before giving up.
const DefaultFanOutTimeout = 5 * time.Second

// NewServer builds an Order Service over the given clients.
func NewServer(clients Clients) *Server {
	if clients.Pricing == nil {
		clients.Pricing = pricing.Unconfigured{}
	}
	return &Server{
		clients:       clients,
		fanOutTimeout: DefaultFanOutTimeout,
		orders:        make(map[string]*pb.Order),
		byCustomer:    make(map[string][]string),
	}
}

// WithFanOutTimeout overrides the fan-out budget, which the
// integration tests use to keep timeout cases quick.
func (s *Server) WithFanOutTimeout(timeout time.Duration) *Server {
	s.fanOutTimeout = timeout
	return s
}

// CreateOrder validates the customer and product, prices the
// order with the serverless pricing function, reserves stock,
// charges the priced total, and best-effort notifies the
// customer, in that order.
//
// Pricing comes before the stock reservation so an order the
// pricing function refuses never holds stock.
//
// Note: if payment fails after stock has been reserved, the
// reservation is not rolled back. A production system would
// need a saga/compensation step here; that is out of scope
// for this lab.
func (s *Server) CreateOrder(
	ctx context.Context,
	req *pb.CreateOrderRequest,
) (*pb.Order, error) {

	if req.GetCustomerId() == "" || req.GetProductId() == "" {
		return nil, status.Error(
			codes.InvalidArgument,
			"customer ID and product ID are required",
		)
	}
	if req.GetQuantity() <= 0 {
		return nil, status.Error(
			codes.InvalidArgument,
			"quantity must be greater than zero",
		)
	}

	ctx, cancel := context.WithTimeout(ctx, s.fanOutTimeout)
	defer cancel()

	customer, err := s.clients.Customer.GetCustomer(ctx, &pb.GetCustomerRequest{
		CustomerId: req.GetCustomerId(),
	})
	if err != nil {
		return nil, err
	}

	product, err := s.clients.Product.GetProduct(ctx, &pb.GetProductRequest{
		ProductId: req.GetProductId(),
	})
	if err != nil {
		return nil, err
	}

	quote, err := s.price(ctx, product, req.GetQuantity())
	if err != nil {
		return nil, err
	}

	if _, err := s.clients.Inventory.ReserveStock(ctx, &pb.ReserveStockRequest{
		ProductId: req.GetProductId(),
		Quantity:  req.GetQuantity(),
	}); err != nil {
		return nil, err
	}

	totalPrice := quote.Total

	s.mu.Lock()
	s.nextID++
	orderID := fmt.Sprintf("ORD%04d", s.nextID)
	s.mu.Unlock()

	paymentResult, err := s.clients.Payment.ProcessPayment(ctx, &pb.PaymentRequest{
		OrderId:    orderID,
		CustomerId: req.GetCustomerId(),
		Amount:     totalPrice,
	})
	if err != nil {
		return nil, err
	}
	log.Printf("payment %s for order %s: %s", paymentResult.GetPaymentId(), orderID, paymentResult.GetStatus())

	notifyMsg := fmt.Sprintf(
		"Hi %s, your order %s for %d x %s (Nu. %.2f total) is confirmed.",
		customer.GetName(), orderID, req.GetQuantity(), product.GetName(), totalPrice,
	)
	if quote.Discount > 0 {
		notifyMsg += fmt.Sprintf(" You saved Nu. %.2f.", quote.Discount)
	}
	if _, err := s.clients.Notification.Notify(ctx, &pb.Notification{
		CustomerId: req.GetCustomerId(),
		Message:    notifyMsg,
	}); err != nil {
		// Best-effort: a notification failure shouldn't roll back a paid order.
		log.Printf("warning: failed to notify customer %s: %v", req.GetCustomerId(), err)
	}

	order := &pb.Order{
		OrderId:      orderID,
		CustomerId:   req.GetCustomerId(),
		ProductId:    req.GetProductId(),
		ProductName:  product.GetName(),
		Quantity:     req.GetQuantity(),
		TotalPrice:   totalPrice,
		Status:       "CONFIRMED",
		PaymentId:    paymentResult.GetPaymentId(),
		Subtotal:     quote.Subtotal,
		Discount:     quote.Discount,
		DeliveryFee:  quote.DeliveryFee,
		PricingRules: quote.AppliedRules,
	}

	s.mu.Lock()
	s.orders[orderID] = order
	s.byCustomer[req.GetCustomerId()] = append(s.byCustomer[req.GetCustomerId()], orderID)
	s.mu.Unlock()

	return order, nil
}

// QuoteOrder prices an order without placing it: the product
// comes from the Product Service and the price from the
// serverless pricing function, exactly as CreateOrder does, but
// nothing is reserved, charged or recorded.
func (s *Server) QuoteOrder(
	ctx context.Context,
	req *pb.QuoteOrderRequest,
) (*pb.OrderQuote, error) {

	if req.GetProductId() == "" {
		return nil, status.Error(codes.InvalidArgument, "product ID is required")
	}
	if req.GetQuantity() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "quantity must be greater than zero")
	}

	ctx, cancel := context.WithTimeout(ctx, s.fanOutTimeout)
	defer cancel()

	product, err := s.clients.Product.GetProduct(ctx, &pb.GetProductRequest{
		ProductId: req.GetProductId(),
	})
	if err != nil {
		return nil, err
	}

	quote, err := s.price(ctx, product, req.GetQuantity())
	if err != nil {
		return nil, err
	}

	return &pb.OrderQuote{
		ProductId:      product.GetProductId(),
		ProductName:    product.GetName(),
		Quantity:       quote.Quantity,
		UnitPrice:      quote.UnitPrice,
		Subtotal:       quote.Subtotal,
		DiscountRate:   quote.DiscountRate,
		Discount:       quote.Discount,
		DeliveryFee:    quote.DeliveryFee,
		Total:          quote.Total,
		PricingRules:   quote.AppliedRules,
		PricingVersion: quote.PricingVersion,
	}, nil
}

// price sends the product details the Product Service returned
// to the serverless pricing function.
func (s *Server) price(ctx context.Context, product *pb.Product, quantity int32) (*pricing.Quote, error) {
	quote, err := s.clients.Pricing.Price(ctx, pricing.Request{
		ProductID: product.GetProductId(),
		Category:  product.GetCategory(),
		UnitPrice: product.GetPrice(),
		Quantity:  quantity,
	})
	if err != nil {
		log.Printf("pricing %s x %d failed: %v", product.GetProductId(), quantity, err)
		return nil, err
	}
	log.Printf("priced %s x %d: subtotal %.2f, discount %.2f, delivery %.2f, total %.2f (pricing %s)",
		product.GetProductId(), quantity, quote.Subtotal, quote.Discount, quote.DeliveryFee,
		quote.Total, quote.PricingVersion)
	return quote, nil
}

// ListOrdersByCustomer streams every stored order placed by
// the given customer.
func (s *Server) ListOrdersByCustomer(
	req *pb.ListOrdersRequest,
	stream pb.OrderService_ListOrdersByCustomerServer,
) error {

	if req.GetCustomerId() == "" {
		return status.Error(codes.InvalidArgument, "customer ID is required")
	}

	s.mu.Lock()
	orderIDs := append([]string(nil), s.byCustomer[req.GetCustomerId()]...)
	s.mu.Unlock()

	for _, orderID := range orderIDs {
		s.mu.Lock()
		order := s.orders[orderID]
		s.mu.Unlock()

		if err := stream.Send(order); err != nil {
			return err
		}
	}

	return nil
}
