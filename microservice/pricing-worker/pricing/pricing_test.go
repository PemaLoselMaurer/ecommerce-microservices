package pricing

import (
	"reflect"
	"testing"
)

func ptr[T any](v T) *T { return &v }

func req(productID, category string, unitPrice, quantity float64) Request {
	return Request{ProductID: &productID, Category: &category, UnitPrice: &unitPrice, Quantity: &quantity}
}

func TestCalculatePricesOrders(t *testing.T) {
	cases := []struct {
		name                                      string
		in                                        Request
		subtotal, rate, discount, delivery, total float64
		rules                                     []string
	}{
		{
			name:     "single cheap item pays delivery",
			in:       req("P003", "accessories", 1800, 1),
			subtotal: 1800, rate: 0, discount: 0, delivery: 150, total: 1950,
			rules: []string{"DELIVERY_FEE: Nu. 150 below Nu. 5,000"},
		},
		{
			name:     "two keyboards reach free delivery",
			in:       req("P002", "accessories", 4500, 2),
			subtotal: 9000, rate: 0, discount: 0, delivery: 0, total: 9000,
			rules: []string{"FREE_DELIVERY: order of Nu. 5,000 or more"},
		},
		{
			name:     "three units earn 5 percent",
			in:       req("P002", "accessories", 4500, 3),
			subtotal: 13500, rate: 0.05, discount: 675, delivery: 0, total: 12825,
			rules: []string{"BULK_5: 5% off for 3 or more units", "FREE_DELIVERY: order of Nu. 5,000 or more"},
		},
		{
			name:     "five units earn 10 percent",
			in:       req("P004", "displays", 25000, 5),
			subtotal: 125000, rate: 0.10, discount: 12500, delivery: 0, total: 112500,
			rules: []string{"BULK_10: 10% off for 5 or more units", "FREE_DELIVERY: order of Nu. 5,000 or more"},
		},
		{
			name:     "discount that drops below the threshold pays delivery",
			in:       req("X", "misc", 1700, 3),
			subtotal: 5100, rate: 0.05, discount: 255, delivery: 150, total: 4995,
			rules: []string{"BULK_5: 5% off for 3 or more units", "DELIVERY_FEE: Nu. 150 below Nu. 5,000"},
		},
		{
			name:     "fractional prices round half up",
			in:       req("X", "misc", 33.33, 3),
			subtotal: 99.99, rate: 0.05, discount: 5, delivery: 150, total: 244.99,
			rules: []string{"BULK_5: 5% off for 3 or more units", "DELIVERY_FEE: Nu. 150 below Nu. 5,000"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, perr := Calculate(tc.in)
			if perr != nil {
				t.Fatalf("unexpected error: %v", perr)
			}
			if got.Subtotal != tc.subtotal || got.DiscountRate != tc.rate || got.Discount != tc.discount ||
				got.DeliveryFee != tc.delivery || got.Total != tc.total {
				t.Errorf("got subtotal=%v rate=%v discount=%v delivery=%v total=%v, want %v %v %v %v %v",
					got.Subtotal, got.DiscountRate, got.Discount, got.DeliveryFee, got.Total,
					tc.subtotal, tc.rate, tc.discount, tc.delivery, tc.total)
			}
			if !reflect.DeepEqual(got.AppliedRules, tc.rules) {
				t.Errorf("rules = %q, want %q", got.AppliedRules, tc.rules)
			}
			if got.Currency != "BTN" || got.PricingVersion != Version {
				t.Errorf("currency/version = %q/%q", got.Currency, got.PricingVersion)
			}
		})
	}
}

func TestCalculateTrimsIdentifiers(t *testing.T) {
	got, perr := Calculate(req("  P001 ", " laptops ", 75000, 1))
	if perr != nil {
		t.Fatal(perr)
	}
	if got.ProductID != "P001" || got.Category != "laptops" {
		t.Errorf("got %q / %q", got.ProductID, got.Category)
	}
}

func TestCalculateRejectsInvalidInput(t *testing.T) {
	valid := req("P001", "laptops", 75000, 1)
	with := func(edit func(*Request)) Request { r := valid; edit(&r); return r }

	cases := []struct {
		name  string
		in    Request
		field string
	}{
		{"missing product_id", with(func(r *Request) { r.ProductID = nil }), "product_id"},
		{"blank product_id", with(func(r *Request) { r.ProductID = ptr("   ") }), "product_id"},
		{"missing category", with(func(r *Request) { r.Category = nil }), "category"},
		{"missing unit_price", with(func(r *Request) { r.UnitPrice = nil }), "unit_price"},
		{"zero unit_price", with(func(r *Request) { r.UnitPrice = ptr(0.0) }), "unit_price"},
		{"negative unit_price", with(func(r *Request) { r.UnitPrice = ptr(-10.0) }), "unit_price"},
		{"missing quantity", with(func(r *Request) { r.Quantity = nil }), "quantity"},
		{"zero quantity", with(func(r *Request) { r.Quantity = ptr(0.0) }), "quantity"},
		{"negative quantity", with(func(r *Request) { r.Quantity = ptr(-2.0) }), "quantity"},
		{"fractional quantity", with(func(r *Request) { r.Quantity = ptr(2.5) }), "quantity"},
		{"quantity over the cap", with(func(r *Request) { r.Quantity = ptr(101.0) }), "quantity"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, perr := Calculate(tc.in)
			if perr == nil {
				t.Fatalf("expected an error, got %+v", got)
			}
			if perr.Status != 400 || perr.Code != "INVALID_INPUT" || perr.Field != tc.field {
				t.Errorf("got %d %s field=%q, want 400 INVALID_INPUT field=%q", perr.Status, perr.Code, perr.Field, tc.field)
			}
		})
	}
}

func TestCalculateRefusesOrdersAboveTheLimit(t *testing.T) {
	// 20 laptops: 1,500,000 less 10% is 1,350,000, above the 1,000,000 cap.
	_, perr := Calculate(req("P001", "laptops", 75000, 20))
	if perr == nil || perr.Status != 422 || perr.Code != "PRICE_LIMIT_EXCEEDED" {
		t.Fatalf("got %+v, want 422 PRICE_LIMIT_EXCEEDED", perr)
	}
	if perr.Message != "order total Nu. 1,350,000 exceeds the maximum of Nu. 1,000,000" {
		t.Errorf("message = %q", perr.Message)
	}
}
