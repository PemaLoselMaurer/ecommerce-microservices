package order

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"ecommerce-microservices/internal/pricing"
	pb "ecommerce-microservices/proto"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// These tests cover the Order Service's use of the serverless
// pricing function: what it sends, what it does with the answer,
// and what happens when there is no answer.

// keyboardQuote is what the real Worker returns for 3 × P002.
func keyboardQuote(req pricing.Request) *pricing.Quote {
	return &pricing.Quote{
		ProductID: req.ProductID, Quantity: req.Quantity, UnitPrice: req.UnitPrice,
		Subtotal: 13500, DiscountRate: 0.05, Discount: 675, DeliveryFee: 0, Total: 12825,
		Currency: "BTN", PricingVersion: "2026-10-01",
		AppliedRules: []string{"BULK_5: 5% off for 3 or more units", "FREE_DELIVERY: order of Nu. 5,000 or more"},
	}
}

func TestCreateOrderSendsProductServiceDataToPricing(t *testing.T) {
	kit := newTestKit()

	if _, err := kit.server.CreateOrder(context.Background(), &pb.CreateOrderRequest{
		CustomerId: "C001", ProductId: "P002", Quantity: 3,
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if kit.pricing.calls != 1 {
		t.Fatalf("pricing was called %d times, want 1", kit.pricing.calls)
	}
	// Price and category come from the Product Service, not the
	// caller, so a client cannot choose its own price.
	want := pricing.Request{ProductID: "P002", Category: "accessories", UnitPrice: 4500, Quantity: 3}
	if got := kit.pricing.requests[0]; got != want {
		t.Errorf("pricing request = %+v, want %+v", got, want)
	}
}

func TestCreateOrderChargesAndRecordsThePricedTotal(t *testing.T) {
	kit := newTestKit()
	kit.pricing.quote = keyboardQuote

	order, err := kit.server.CreateOrder(context.Background(), &pb.CreateOrderRequest{
		CustomerId: "C001", ProductId: "P002", Quantity: 3,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(kit.payment.charged) != 1 || kit.payment.charged[0] != 12825 {
		t.Errorf("charged %v, want [12825] from the pricing function", kit.payment.charged)
	}
	if order.GetTotalPrice() != 12825 || order.GetSubtotal() != 13500 ||
		order.GetDiscount() != 675 || order.GetDeliveryFee() != 0 {
		t.Errorf("order breakdown = total %v subtotal %v discount %v delivery %v",
			order.GetTotalPrice(), order.GetSubtotal(), order.GetDiscount(), order.GetDeliveryFee())
	}
	if !reflect.DeepEqual(order.GetPricingRules(), keyboardQuote(pricing.Request{}).AppliedRules) {
		t.Errorf("pricing rules = %q", order.GetPricingRules())
	}

	// The stored order, as order history shows it, carries the
	// same breakdown.
	stream := &recordingOrderStream{}
	if err := kit.server.ListOrdersByCustomer(&pb.ListOrdersRequest{CustomerId: "C001"}, stream); err != nil {
		t.Fatal(err)
	}
	if len(stream.sent) != 1 || stream.sent[0].GetDiscount() != 675 {
		t.Errorf("history = %v", stream.sent)
	}

	if msg := kit.notification.sent[0].GetMessage(); !strings.Contains(msg, "Nu. 12825.00") || !strings.Contains(msg, "saved Nu. 675.00") {
		t.Errorf("notification %q does not mention the priced total and saving", msg)
	}
}

func TestCreateOrderStopsWhenPricingFails(t *testing.T) {
	for _, code := range []codes.Code{
		codes.InvalidArgument, codes.FailedPrecondition, codes.PermissionDenied,
		codes.Unavailable, codes.DeadlineExceeded, codes.Internal,
	} {
		t.Run(code.String(), func(t *testing.T) {
			kit := newTestKit()
			kit.pricing.err = status.Error(code, "pricing failed")

			_, err := kit.server.CreateOrder(context.Background(), &pb.CreateOrderRequest{
				CustomerId: "C001", ProductId: "P002", Quantity: 1,
			})
			assertCode(t, err, code)

			// Pricing runs before stock is reserved, so a failed
			// price holds no stock and charges nothing.
			if kit.inventory.calls != 0 || kit.payment.calls != 0 || kit.notification.calls != 0 {
				t.Errorf("after a pricing failure: inventory %d, payment %d, notification %d calls; want 0",
					kit.inventory.calls, kit.payment.calls, kit.notification.calls)
			}
			if kit.inventory.stock["P002"] != 25 {
				t.Errorf("stock changed to %d", kit.inventory.stock["P002"])
			}
		})
	}
}

func TestCreateOrderIsRefusedWithoutAPricingFunction(t *testing.T) {
	kit := newTestKit()
	server := NewServer(Clients{
		Customer: kit.customer, Product: kit.product, Inventory: kit.inventory,
		Payment: kit.payment, Notification: kit.notification,
	})

	_, err := server.CreateOrder(context.Background(), &pb.CreateOrderRequest{
		CustomerId: "C001", ProductId: "P002", Quantity: 1,
	})
	assertCode(t, err, codes.Unavailable)
	if kit.payment.calls != 0 {
		t.Error("an order was charged without being priced")
	}
}

func TestQuoteOrderReturnsTheBreakdownWithoutPlacingAnything(t *testing.T) {
	kit := newTestKit()
	kit.pricing.quote = keyboardQuote

	quote, err := kit.server.QuoteOrder(context.Background(), &pb.QuoteOrderRequest{ProductId: "P002", Quantity: 3})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if quote.GetProductName() != "Mechanical Keyboard" || quote.GetTotal() != 12825 ||
		quote.GetDiscountRate() != 0.05 || quote.GetPricingVersion() != "2026-10-01" {
		t.Errorf("quote = %+v", quote)
	}
	if kit.customer.calls != 0 || kit.inventory.calls != 0 || kit.payment.calls != 0 || kit.notification.calls != 0 {
		t.Error("a quote touched customer, inventory, payment or notification")
	}
}

func TestQuoteOrderValidation(t *testing.T) {
	cases := []struct {
		name string
		in   *pb.QuoteOrderRequest
		want codes.Code
	}{
		{"missing product", &pb.QuoteOrderRequest{Quantity: 1}, codes.InvalidArgument},
		{"zero quantity", &pb.QuoteOrderRequest{ProductId: "P002"}, codes.InvalidArgument},
		{"unknown product", &pb.QuoteOrderRequest{ProductId: "P999", Quantity: 1}, codes.NotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kit := newTestKit()
			_, err := kit.server.QuoteOrder(context.Background(), tc.in)
			assertCode(t, err, tc.want)
			if kit.pricing.calls != 0 {
				t.Error("pricing was called for a request that should have been rejected first")
			}
		})
	}
}
