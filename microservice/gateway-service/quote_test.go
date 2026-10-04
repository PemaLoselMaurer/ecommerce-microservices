package main

import (
	"encoding/json"
	"net/http"
	"testing"

	pb "ecommerce-microservices/proto"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// POST /api/orders/quote relays the Order Service's price, which
// comes from the serverless pricing function.

func TestQuoteRelaysTheBreakdown(t *testing.T) {
	kit := newGatewayKit()

	response := kit.do(http.MethodPost, "/api/orders/quote", `{"product_id":" P002 ","quantity":3}`)
	if response.Code != http.StatusOK {
		t.Fatalf("status %d: %s", response.Code, response.Body.String())
	}

	var quote quoteView
	if err := json.Unmarshal(response.Body.Bytes(), &quote); err != nil {
		t.Fatal(err)
	}
	if quote.ProductID != "P002" || quote.Subtotal != 3000 || quote.Discount != 150 ||
		quote.DeliveryFee != 150 || quote.Total != 3000 {
		t.Errorf("quote = %+v", quote)
	}
	if len(kit.order.quoted) != 1 || kit.order.quoted[0].GetProductId() != "P002" {
		t.Errorf("Order Service saw %v", kit.order.quoted)
	}
}

func TestQuoteNeedsNoSession(t *testing.T) {
	kit := newGatewayKit()
	if response := kit.do(http.MethodPost, "/api/orders/quote", `{"product_id":"P002","quantity":1}`); response.Code != http.StatusOK {
		t.Fatalf("status %d, want 200 without signing in", response.Code)
	}
}

func TestQuoteRejectsBadBodiesBeforeCallingTheOrderService(t *testing.T) {
	for name, body := range map[string]string{
		"not JSON":      `nope`,
		"no product":    `{"quantity":1}`,
		"zero quantity": `{"product_id":"P002","quantity":0}`,
	} {
		t.Run(name, func(t *testing.T) {
			kit := newGatewayKit()
			response := kit.do(http.MethodPost, "/api/orders/quote", body)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status %d, want 400", response.Code)
			}
			if len(kit.order.quoted) != 0 {
				t.Error("the Order Service was called")
			}
		})
	}
}

func TestQuoteMapsPricingFailures(t *testing.T) {
	cases := []struct {
		code codes.Code
		want int
	}{
		{codes.FailedPrecondition, http.StatusConflict},
		{codes.Unavailable, http.StatusServiceUnavailable},
		{codes.DeadlineExceeded, http.StatusGatewayTimeout},
		{codes.PermissionDenied, http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.code.String(), func(t *testing.T) {
			kit := newGatewayKit()
			kit.order.quoteErr = status.Error(tc.code, "pricing failed")

			response := kit.do(http.MethodPost, "/api/orders/quote", `{"product_id":"P002","quantity":1}`)
			if response.Code != tc.want {
				t.Fatalf("status %d, want %d", response.Code, tc.want)
			}
			if view := decodeError(t, response); view.Error == "" || view.Code != tc.code.String() {
				t.Errorf("error view = %+v", view)
			}
		})
	}
}

func TestPlacedOrderCarriesTheBreakdown(t *testing.T) {
	view := toOrderView(&pb.Order{
		OrderId: "ORD0001", TotalPrice: 12825, Subtotal: 13500, Discount: 675,
		PricingRules: []string{"BULK_5"},
	})
	if view.Subtotal != 13500 || view.Discount != 675 || view.TotalPrice != 12825 || len(view.PricingRules) != 1 {
		t.Errorf("view = %+v", view)
	}
	if rules := toOrderView(&pb.Order{}).PricingRules; rules == nil {
		t.Error("an order with no rules should serialise them as [], not null")
	}
}
