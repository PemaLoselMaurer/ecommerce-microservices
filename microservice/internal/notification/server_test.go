package notification

import (
	"context"
	"testing"
	"time"

	pb "ecommerce-microservices/proto"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// These tests exercise NotificationServiceServer directly, in
// process, with no gRPC server listening.

func newTestServer() *Server {
	return NewServer()
}

func assertCode(t *testing.T, err error, want codes.Code) {
	t.Helper()

	if err == nil {
		t.Fatalf("expected error with code %s, got nil", want)
	}

	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected a gRPC status error, got %T: %v", err, err)
	}
	if st.Code() != want {
		t.Fatalf("expected code %s, got %s (%q)", want, st.Code(), st.Message())
	}
}

// ---------- Notify ----------

func TestNotifySucceedsWithNoSubscribers(t *testing.T) {
	server := newTestServer()

	// Nobody is listening, which is the normal case when an
	// order is placed from a page that is not streaming. The
	// call must still succeed, reporting that nothing was
	// delivered — the Order Service treats notification as
	// best-effort and must not fail a paid order over it.
	response, err := server.Notify(context.Background(), &pb.Notification{
		CustomerId: "C001",
		Message:    "Your order is confirmed.",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if response.GetDelivered() {
		t.Error("delivered: got true, want false when nobody is subscribed")
	}
}

func TestNotifyDeliversToASubscriber(t *testing.T) {
	server := newTestServer()

	inbox := make(chan *pb.Notification, 1)
	server.subscribers["C001"] = []chan *pb.Notification{inbox}

	response, err := server.Notify(context.Background(), &pb.Notification{
		CustomerId: "C001",
		Message:    "Your order is confirmed.",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !response.GetDelivered() {
		t.Error("delivered: got false, want true")
	}

	select {
	case notification := <-inbox:
		if notification.GetCustomerId() != "C001" {
			t.Errorf("customer ID: got %q, want %q", notification.GetCustomerId(), "C001")
		}
		if notification.GetMessage() != "Your order is confirmed." {
			t.Errorf("message: got %q, want %q", notification.GetMessage(), "Your order is confirmed.")
		}
		if notification.GetTimestamp() == 0 {
			t.Error("timestamp: got 0, want the time the notification was created")
		}
	default:
		t.Fatal("nothing arrived in the subscriber's inbox")
	}
}

func TestNotifyReachesEverySubscriberForThatCustomer(t *testing.T) {
	server := newTestServer()

	// One customer signed in on two devices.
	first := make(chan *pb.Notification, 1)
	second := make(chan *pb.Notification, 1)
	server.subscribers["C001"] = []chan *pb.Notification{first, second}

	if _, err := server.Notify(context.Background(), &pb.Notification{
		CustomerId: "C001", Message: "Your order is confirmed.",
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for name, inbox := range map[string]chan *pb.Notification{"first": first, "second": second} {
		if len(inbox) != 1 {
			t.Errorf("%s subscriber received %d notifications, want 1", name, len(inbox))
		}
	}
}

func TestNotifyDoesNotReachOtherCustomers(t *testing.T) {
	server := newTestServer()

	alice := make(chan *pb.Notification, 1)
	bob := make(chan *pb.Notification, 1)
	server.subscribers["C001"] = []chan *pb.Notification{alice}
	server.subscribers["C002"] = []chan *pb.Notification{bob}

	if _, err := server.Notify(context.Background(), &pb.Notification{
		CustomerId: "C001", Message: "Your order is confirmed.",
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(alice) != 1 {
		t.Errorf("the addressed customer received %d notifications, want 1", len(alice))
	}
	if len(bob) != 0 {
		t.Errorf("another customer received %d notifications, want 0", len(bob))
	}
}

func TestNotifyRejectsIncompleteRequests(t *testing.T) {
	cases := []struct {
		name    string
		request *pb.Notification
	}{
		{"no customer ID", &pb.Notification{Message: "hello"}},
		{"no message", &pb.Notification{CustomerId: "C001"}},
		{"neither", &pb.Notification{}},
		{"nil request", nil},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			server := newTestServer()

			_, err := server.Notify(context.Background(), testCase.request)

			assertCode(t, err, codes.InvalidArgument)
		})
	}
}

func TestNotifyDoesNotBlockOnAFullInbox(t *testing.T) {
	server := newTestServer()

	// A subscriber that has stopped reading must not be able to
	// stall the Order Service. The send is non-blocking, so the
	// notification is dropped for that subscriber instead.
	full := make(chan *pb.Notification) // unbuffered, nobody receiving
	server.subscribers["C001"] = []chan *pb.Notification{full}

	done := make(chan struct{})
	go func() {
		_, _ = server.Notify(context.Background(), &pb.Notification{
			CustomerId: "C001", Message: "Your order is confirmed.",
		})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Notify blocked on a subscriber that was not reading")
	}
}

func TestNotifyTimestampsEachNotification(t *testing.T) {
	server := newTestServer()

	inbox := make(chan *pb.Notification, 1)
	server.subscribers["C001"] = []chan *pb.Notification{inbox}

	before := time.Now().Unix()

	if _, err := server.Notify(context.Background(), &pb.Notification{
		CustomerId: "C001", Message: "Your order is confirmed.",
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	notification := <-inbox
	after := time.Now().Unix()

	if notification.GetTimestamp() < before || notification.GetTimestamp() > after {
		t.Errorf("timestamp %d is outside the window [%d, %d]",
			notification.GetTimestamp(), before, after)
	}
}

// ---------- unsubscribe ----------

func TestUnsubscribeRemovesOnlyThatSubscriber(t *testing.T) {
	server := newTestServer()

	first := make(chan *pb.Notification, 1)
	second := make(chan *pb.Notification, 1)
	server.subscribers["C001"] = []chan *pb.Notification{first, second}

	server.unsubscribe("C001", first)

	remaining := server.subscribers["C001"]
	if len(remaining) != 1 {
		t.Fatalf("%d subscribers remain, want 1", len(remaining))
	}
	if remaining[0] != second {
		t.Error("the wrong subscriber was removed")
	}
}

func TestUnsubscribeIsSafeWhenTheSubscriberIsAlreadyGone(t *testing.T) {
	server := newTestServer()

	inbox := make(chan *pb.Notification, 1)
	server.subscribers["C001"] = []chan *pb.Notification{inbox}

	// Removing twice, and removing from a customer that has no
	// subscribers at all, must both be no-ops rather than
	// panics — a disconnecting client can trigger either.
	server.unsubscribe("C001", inbox)
	server.unsubscribe("C001", inbox)
	server.unsubscribe("C999", inbox)

	if len(server.subscribers["C001"]) != 0 {
		t.Errorf("%d subscribers remain, want 0", len(server.subscribers["C001"]))
	}
}

func TestNotifyAfterUnsubscribeDeliversNothing(t *testing.T) {
	server := newTestServer()

	inbox := make(chan *pb.Notification, 1)
	server.subscribers["C001"] = []chan *pb.Notification{inbox}
	server.unsubscribe("C001", inbox)

	response, err := server.Notify(context.Background(), &pb.Notification{
		CustomerId: "C001", Message: "Your order is confirmed.",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if response.GetDelivered() {
		t.Error("delivered: got true, want false after the subscriber left")
	}
	if len(inbox) != 0 {
		t.Errorf("the departed subscriber received %d notifications, want 0", len(inbox))
	}
}
