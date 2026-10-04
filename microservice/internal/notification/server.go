// Package notification holds the Notification Service
// implementation.
package notification

import (
	"context"
	"sync"
	"time"

	pb "ecommerce-microservices/proto"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Server implements the NotificationService defined in
// notification.proto.
type Server struct {
	pb.UnimplementedNotificationServiceServer

	mu          sync.Mutex
	subscribers map[string][]chan *pb.Notification
}

// NewServer builds a Notification Service with no subscribers.
func NewServer() *Server {
	return &Server{subscribers: make(map[string][]chan *pb.Notification)}
}

// Notify pushes a notification to a customer, fanning it out
// to any currently subscribed streams for that customer.
func (s *Server) Notify(
	ctx context.Context,
	req *pb.Notification,
) (*pb.NotifyResponse, error) {

	if req.GetCustomerId() == "" || req.GetMessage() == "" {
		return nil, status.Error(
			codes.InvalidArgument,
			"customer ID and message are required",
		)
	}

	notification := &pb.Notification{
		CustomerId: req.GetCustomerId(),
		Message:    req.GetMessage(),
		Timestamp:  time.Now().Unix(),
	}

	s.mu.Lock()
	subscribers := append([]chan *pb.Notification(nil), s.subscribers[req.GetCustomerId()]...)
	s.mu.Unlock()

	delivered := false
	for _, ch := range subscribers {
		select {
		case ch <- notification:
			delivered = true
		default:
		}
	}

	return &pb.NotifyResponse{Delivered: delivered}, nil
}

// SubscribeNotifications streams live notifications for a
// customer until the client disconnects.
func (s *Server) SubscribeNotifications(
	req *pb.SubscribeRequest,
	stream pb.NotificationService_SubscribeNotificationsServer,
) error {

	customerID := req.GetCustomerId()
	if customerID == "" {
		return status.Error(codes.InvalidArgument, "customer ID is required")
	}

	ch := make(chan *pb.Notification, 8)

	s.mu.Lock()
	s.subscribers[customerID] = append(s.subscribers[customerID], ch)
	s.mu.Unlock()

	defer s.unsubscribe(customerID, ch)

	for {
		select {
		case <-stream.Context().Done():
			return stream.Context().Err()
		case notification := <-ch:
			if err := stream.Send(notification); err != nil {
				return err
			}
		}
	}
}

func (s *Server) unsubscribe(customerID string, ch chan *pb.Notification) {
	s.mu.Lock()
	defer s.mu.Unlock()

	subscribers := s.subscribers[customerID]
	for i, c := range subscribers {
		if c == ch {
			s.subscribers[customerID] = append(subscribers[:i], subscribers[i+1:]...)
			break
		}
	}
}
