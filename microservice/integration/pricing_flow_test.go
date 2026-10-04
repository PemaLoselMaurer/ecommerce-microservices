package integration

import (
	"testing"

	pb "ecommerce-microservices/proto"
)

// Lab 5, Part C: the serverless pricing function in the
// application's own flow.
//
//   client → Order Service → Product Service (unit price, category)
//                          → pricing Worker (over HTTP)
//                          → Inventory, Payment (charges the Worker's total)
//                          → result returned to the client
//
// The Worker here is the same handler code deployed to
// Cloudflare, reached through the Order Service's production
// HTTP client.

func TestOrderIsPricedByTheWorkerAndChargedItsTotal(t *testing.T) {
	sys := newSystem(t, defaultOptions())

	// 3 × Mechanical Keyboard at 4,500: 5% bulk discount, free
	// delivery.
	order, err := sys.order.CreateOrder(callContext(t), &pb.CreateOrderRequest{
		CustomerId: "C001", ProductId: "P002", Quantity: 3,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if order.GetSubtotal() != 13500 || order.GetDiscount() != 675 ||
		order.GetDeliveryFee() != 0 || order.GetTotalPrice() != 12825 {
		t.Errorf("breakdown: subtotal %v discount %v delivery %v total %v; want 13500 675 0 12825",
			order.GetSubtotal(), order.GetDiscount(), order.GetDeliveryFee(), order.GetTotalPrice())
	}
	if len(order.GetPricingRules()) != 2 {
		t.Errorf("pricing rules = %q, want the bulk and delivery rules", order.GetPricingRules())
	}
	if order.GetPaymentId() == "" || order.GetStatus() != "CONFIRMED" {
		t.Errorf("payment %q status %q", order.GetPaymentId(), order.GetStatus())
	}
}

func TestDeliveryFeeIsAddedBelowTheThreshold(t *testing.T) {
	sys := newSystem(t, defaultOptions())

	// 1 × Wireless Mouse at 1,800 is under Nu. 5,000.
	order, err := sys.order.CreateOrder(callContext(t), &pb.CreateOrderRequest{
		CustomerId: "C002", ProductId: "P003", Quantity: 1,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if order.GetDeliveryFee() != 150 || order.GetTotalPrice() != 1950 {
		t.Errorf("delivery %v total %v, want 150 and 1950", order.GetDeliveryFee(), order.GetTotalPrice())
	}
}

func TestQuoteMatchesWhatTheOrderIsCharged(t *testing.T) {
	sys := newSystem(t, defaultOptions())
	stockClient := pb.NewInventoryServiceClient(sys.inventorySvc.dial(t))

	before, err := stockClient.GetStock(callContext(t), &pb.GetStockRequest{ProductId: "P004"})
	if err != nil {
		t.Fatal(err)
	}

	quote, err := sys.order.QuoteOrder(callContext(t), &pb.QuoteOrderRequest{ProductId: "P004", Quantity: 5})
	if err != nil {
		t.Fatalf("quote: %v", err)
	}
	// 5 × Monitor at 25,000: 10% off.
	if quote.GetProductName() != "Monitor" || quote.GetDiscountRate() != 0.10 || quote.GetTotal() != 112500 {
		t.Errorf("quote = %v", quote)
	}

	// A quote reserves nothing.
	after, err := stockClient.GetStock(callContext(t), &pb.GetStockRequest{ProductId: "P004"})
	if err != nil {
		t.Fatal(err)
	}
	if after.GetQuantity() != before.GetQuantity() {
		t.Errorf("a quote changed stock from %d to %d", before.GetQuantity(), after.GetQuantity())
	}

	// Placing the same order is charged exactly the quoted total.
	// 112,500 is above the Payment Service's decline threshold,
	// so place a smaller order to compare: 3 monitors.
	quote3, err := sys.order.QuoteOrder(callContext(t), &pb.QuoteOrderRequest{ProductId: "P004", Quantity: 3})
	if err != nil {
		t.Fatal(err)
	}
	order, err := sys.order.CreateOrder(callContext(t), &pb.CreateOrderRequest{
		CustomerId: "C001", ProductId: "P004", Quantity: 3,
	})
	if err != nil {
		t.Fatalf("order: %v", err)
	}
	if order.GetTotalPrice() != quote3.GetTotal() {
		t.Errorf("charged %v, quoted %v", order.GetTotalPrice(), quote3.GetTotal())
	}
}
