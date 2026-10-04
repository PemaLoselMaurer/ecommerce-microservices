// Package inventory holds the Inventory Service
// implementation.
package inventory

import (
	"context"
	"fmt"
	"io"
	"sync"

	pb "ecommerce-microservices/proto"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// LowStockThreshold is the level below which a stock
// adjustment raises a low-stock alert.
const LowStockThreshold = 5

// Server implements the InventoryService defined in
// inventory.proto.
type Server struct {
	pb.UnimplementedInventoryServiceServer

	// flaky enables the simulated transient failures below.
	// Kept as a field rather than hard-coded so tests (and a
	// run with INVENTORY_FLAKY=false) can get a dependable
	// ReserveStock.
	flaky bool

	mu           sync.Mutex
	stock        map[string]int32
	watchers     map[string][]chan *pb.StockItem
	reserveCalls int
}

// NewServer builds an Inventory Service over the given stock
// levels. When flaky is set, ReserveStock fails two calls in
// three, giving the Retry pattern something to recover from.
func NewServer(stock map[string]int32, flaky bool) *Server {
	return &Server{
		flaky:    flaky,
		stock:    stock,
		watchers: make(map[string][]chan *pb.StockItem),
	}
}

// simulateFlakyReserve stands in for an unreliable warehouse
// backend, to give the Retry resilience pattern something to
// demonstrate: two calls out of every three fail with a
// transient Unavailable error, so a client that retries (up to
// 3 attempts) still gets its stock reserved. It runs before any
// state is touched, so retrying is safe.
func (s *Server) simulateFlakyReserve() error {
	if !s.flaky {
		return nil
	}

	s.mu.Lock()
	s.reserveCalls++
	fail := s.reserveCalls%3 != 0
	s.mu.Unlock()

	if fail {
		return status.Error(codes.Unavailable, "inventory backend temporarily unavailable, please retry")
	}
	return nil
}

// GetStock receives a product ID and returns its current
// stock quantity.
func (s *Server) GetStock(
	ctx context.Context,
	req *pb.GetStockRequest,
) (*pb.StockItem, error) {

	s.mu.Lock()
	defer s.mu.Unlock()

	quantity, exists := s.stock[req.GetProductId()]
	if !exists {
		return nil, status.Errorf(
			codes.NotFound,
			"no stock record for product %s",
			req.GetProductId(),
		)
	}

	return &pb.StockItem{ProductId: req.GetProductId(), Quantity: quantity}, nil
}

// ReserveStock decrements stock for a product by the
// requested quantity, failing if not enough is available.
func (s *Server) ReserveStock(
	ctx context.Context,
	req *pb.ReserveStockRequest,
) (*pb.StockItem, error) {

	if req.GetQuantity() <= 0 {
		return nil, status.Error(
			codes.InvalidArgument,
			"quantity must be greater than zero",
		)
	}

	if err := s.simulateFlakyReserve(); err != nil {
		return nil, err
	}

	s.mu.Lock()
	quantity, exists := s.stock[req.GetProductId()]
	if !exists {
		s.mu.Unlock()
		return nil, status.Errorf(
			codes.NotFound,
			"no stock record for product %s",
			req.GetProductId(),
		)
	}
	if quantity < req.GetQuantity() {
		s.mu.Unlock()
		return nil, status.Errorf(
			codes.ResourceExhausted,
			"insufficient stock for product %s: have %d, requested %d",
			req.GetProductId(), quantity, req.GetQuantity(),
		)
	}

	quantity -= req.GetQuantity()
	s.stock[req.GetProductId()] = quantity
	item := &pb.StockItem{ProductId: req.GetProductId(), Quantity: quantity}
	s.mu.Unlock()

	s.publish(item)
	return item, nil
}

// WatchStock streams live stock updates for a single product
// as they happen, until the client disconnects.
func (s *Server) WatchStock(
	req *pb.WatchStockRequest,
	stream pb.InventoryService_WatchStockServer,
) error {

	productID := req.GetProductId()

	s.mu.Lock()
	if _, exists := s.stock[productID]; !exists {
		s.mu.Unlock()
		return status.Errorf(
			codes.NotFound,
			"no stock record for product %s",
			productID,
		)
	}
	ch := make(chan *pb.StockItem, 4)
	s.watchers[productID] = append(s.watchers[productID], ch)
	quantity := s.stock[productID]
	s.mu.Unlock()

	defer s.unsubscribe(productID, ch)

	// Send the current value immediately, then stream updates.
	if err := stream.Send(&pb.StockItem{ProductId: productID, Quantity: quantity}); err != nil {
		return err
	}

	for {
		select {
		case <-stream.Context().Done():
			return stream.Context().Err()
		case item := <-ch:
			if err := stream.Send(item); err != nil {
				return err
			}
		}
	}
}

// SyncStock lets a warehouse client stream a batch of stock
// adjustments and receive a real-time alert for each one.
func (s *Server) SyncStock(
	stream pb.InventoryService_SyncStockServer,
) error {

	for {
		adjustment, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}

		alert, err := s.applyAdjustment(adjustment)
		if err != nil {
			return err
		}
		if err := stream.Send(alert); err != nil {
			return err
		}
	}
}

func (s *Server) applyAdjustment(adj *pb.StockAdjustment) (*pb.StockAlert, error) {
	s.mu.Lock()
	quantity, exists := s.stock[adj.GetProductId()]
	if !exists {
		s.mu.Unlock()
		return nil, status.Errorf(
			codes.NotFound,
			"no stock record for product %s",
			adj.GetProductId(),
		)
	}

	quantity += adj.GetDelta()
	if quantity < 0 {
		quantity = 0
	}
	s.stock[adj.GetProductId()] = quantity
	s.mu.Unlock()

	item := &pb.StockItem{ProductId: adj.GetProductId(), Quantity: quantity}
	s.publish(item)

	lowStock := quantity < LowStockThreshold
	message := "stock updated"
	if lowStock {
		message = fmt.Sprintf("low stock warning: only %d left", quantity)
	}

	return &pb.StockAlert{
		ProductId:   adj.GetProductId(),
		NewQuantity: quantity,
		LowStock:    lowStock,
		Message:     message,
	}, nil
}

// publish fans a stock update out to any active WatchStock
// subscribers for that product.
func (s *Server) publish(item *pb.StockItem) {
	s.mu.Lock()
	subscribers := append([]chan *pb.StockItem(nil), s.watchers[item.GetProductId()]...)
	s.mu.Unlock()

	for _, ch := range subscribers {
		select {
		case ch <- item:
		default:
		}
	}
}

func (s *Server) unsubscribe(productID string, ch chan *pb.StockItem) {
	s.mu.Lock()
	defer s.mu.Unlock()

	subscribers := s.watchers[productID]
	for i, c := range subscribers {
		if c == ch {
			s.watchers[productID] = append(subscribers[:i], subscribers[i+1:]...)
			break
		}
	}
}

// InitialStock returns the stock levels held in memory, one
// entry per product in the Product Service catalogue.
func InitialStock() map[string]int32 {
	return map[string]int32{
		"P001": 10,
		"P002": 25,
		"P003": 30,
		"P004": 8,
		"P005": 14,
		"P006": 6,
		"P007": 3, // deliberately below LowStockThreshold
		"P008": 0, // deliberately out of stock
	}
}
