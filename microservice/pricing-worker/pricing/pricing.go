// Package pricing holds the one responsibility of the
// calculate-order-price Worker: turning a product's unit price
// and an order quantity into a priced order.
//
// It is pure — no I/O, no clock, no data store — so it can be
// tested without Cloudflare, and the Worker can never reach into
// another service's data.
package pricing

import (
	"fmt"
	"strings"
)

// Version identifies the rule set, so a receipt can say which
// rules priced it.
const Version = "2026-10-01"

// Currency is ISO 4217 for the Bhutanese ngultrum.
const Currency = "BTN"

// Amounts are held in chhertum (1/100 ngultrum) so no float
// rounding creeps into the arithmetic.
const (
	FreeDeliveryThreshold int64 = 5_000_00 // matches the storefront
	DeliveryFee           int64 = 150_00
	MaxOrderTotal         int64 = 1_000_000_00
	MaxQuantity                 = 100
)

// tier is a bulk discount: Percent off when at least MinQuantity
// units are ordered. Tiers are listed highest threshold first.
type tier struct {
	MinQuantity int
	Percent     int64
}

var discountTiers = []tier{
	{MinQuantity: 5, Percent: 10},
	{MinQuantity: 3, Percent: 5},
}

// Request is what the Order Service sends. Pointer fields let
// validation tell "missing" apart from "zero".
type Request struct {
	ProductID *string  `json:"product_id"`
	Category  *string  `json:"category"`
	UnitPrice *float64 `json:"unit_price"`
	Quantity  *float64 `json:"quantity"`
}

// Result is the priced order returned on success.
type Result struct {
	ProductID      string   `json:"product_id"`
	Category       string   `json:"category"`
	Quantity       int      `json:"quantity"`
	UnitPrice      float64  `json:"unit_price"`
	Subtotal       float64  `json:"subtotal"`
	DiscountRate   float64  `json:"discount_rate"`
	Discount       float64  `json:"discount"`
	DeliveryFee    float64  `json:"delivery_fee"`
	Total          float64  `json:"total"`
	Currency       string   `json:"currency"`
	AppliedRules   []string `json:"applied_rules"`
	PricingVersion string   `json:"pricing_version"`
}

// Error is a request that could not be priced. Status is the
// HTTP status the Worker answers with.
type Error struct {
	Status  int    `json:"-"`
	Code    string `json:"error"`
	Message string `json:"message"`
	Field   string `json:"field,omitempty"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

func invalid(field, message string) *Error {
	return &Error{Status: 400, Code: "INVALID_INPUT", Message: message, Field: field}
}

// Calculate validates req and prices it.
func Calculate(req Request) (*Result, *Error) {
	if req.ProductID == nil || strings.TrimSpace(*req.ProductID) == "" {
		return nil, invalid("product_id", "product_id is required")
	}
	if req.Category == nil || strings.TrimSpace(*req.Category) == "" {
		return nil, invalid("category", "category is required")
	}
	if req.UnitPrice == nil {
		return nil, invalid("unit_price", "unit_price is required")
	}
	if *req.UnitPrice <= 0 {
		return nil, invalid("unit_price", "unit_price must be greater than zero")
	}
	if req.Quantity == nil {
		return nil, invalid("quantity", "quantity is required")
	}
	q := *req.Quantity
	if q != float64(int64(q)) {
		return nil, invalid("quantity", "quantity must be a whole number")
	}
	if q <= 0 {
		return nil, invalid("quantity", "quantity must be greater than zero")
	}
	if q > MaxQuantity {
		return nil, invalid("quantity", fmt.Sprintf("quantity must not exceed %d", MaxQuantity))
	}

	quantity := int(q)
	unitPrice := toCents(*req.UnitPrice)
	var rules []string

	subtotal := unitPrice * int64(quantity)

	var percent int64
	for _, t := range discountTiers {
		if quantity >= t.MinQuantity {
			percent = t.Percent
			rules = append(rules, fmt.Sprintf("BULK_%d: %d%% off for %d or more units", t.Percent, t.Percent, t.MinQuantity))
			break
		}
	}
	// Round half up to the nearest chhertum.
	discount := (subtotal*percent + 50) / 100

	discounted := subtotal - discount
	deliveryFee := DeliveryFee
	if discounted >= FreeDeliveryThreshold {
		deliveryFee = 0
		rules = append(rules, fmt.Sprintf("FREE_DELIVERY: order of %s or more", nu(FreeDeliveryThreshold)))
	} else {
		rules = append(rules, fmt.Sprintf("DELIVERY_FEE: %s below %s", nu(DeliveryFee), nu(FreeDeliveryThreshold)))
	}

	total := discounted + deliveryFee
	if total > MaxOrderTotal {
		return nil, &Error{
			Status:  422,
			Code:    "PRICE_LIMIT_EXCEEDED",
			Message: fmt.Sprintf("order total %s exceeds the maximum of %s", nu(total), nu(MaxOrderTotal)),
		}
	}

	return &Result{
		ProductID:      strings.TrimSpace(*req.ProductID),
		Category:       strings.TrimSpace(*req.Category),
		Quantity:       quantity,
		UnitPrice:      fromCents(unitPrice),
		Subtotal:       fromCents(subtotal),
		DiscountRate:   float64(percent) / 100,
		Discount:       fromCents(discount),
		DeliveryFee:    fromCents(deliveryFee),
		Total:          fromCents(total),
		Currency:       Currency,
		AppliedRules:   rules,
		PricingVersion: Version,
	}, nil
}

func toCents(amount float64) int64 {
	if amount < 0 {
		return int64(amount*100 - 0.5)
	}
	return int64(amount*100 + 0.5)
}

func fromCents(cents int64) float64 { return float64(cents) / 100 }

func nu(cents int64) string {
	whole := cents / 100
	s := fmt.Sprintf("%d", whole)
	// Group thousands for readability: 1000000 -> 1,000,000.
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	if frac := cents % 100; frac != 0 {
		s += fmt.Sprintf(".%02d", frac)
	}
	return "Nu. " + s
}
