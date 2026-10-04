// Package pricing is the Order Service's client for the
// calculate-order-price serverless function, a Cloudflare Worker.
//
// The Worker speaks HTTP and JSON; the rest of the system speaks
// gRPC. This package is the boundary between the two: every
// outcome of a call — a price, a rejection, an outage, a bad
// credential, a timeout — comes back as either a Quote or a gRPC
// status error, so the Order Service handles the Worker exactly
// as it handles its other dependencies.
package pricing

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Request is what the Worker needs to price an order. Every
// field is obtained from the Product Service or the customer's
// order, never from a data store.
type Request struct {
	ProductID string  `json:"product_id"`
	Category  string  `json:"category"`
	UnitPrice float64 `json:"unit_price"`
	Quantity  int32   `json:"quantity"`
}

// Quote is the Worker's price breakdown.
type Quote struct {
	ProductID      string   `json:"product_id"`
	Quantity       int32    `json:"quantity"`
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

// Client prices orders. It is an interface so the Order
// Service can be tested without the network.
type Client interface {
	Price(ctx context.Context, req Request) (*Quote, error)
}

// DefaultTimeout bounds a single call to the Worker.
const DefaultTimeout = 3 * time.Second

// HTTPClient calls the deployed Worker.
type HTTPClient struct {
	endpoint string
	token    string
	timeout  time.Duration
	http     *http.Client
}

// NewHTTPClient builds a client for the Worker at baseURL,
// authenticating with token. A zero timeout uses DefaultTimeout.
func NewHTTPClient(baseURL, token string, timeout time.Duration) *HTTPClient {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	return &HTTPClient{
		endpoint: strings.TrimRight(baseURL, "/") + "/price",
		token:    token,
		timeout:  timeout,
		http:     &http.Client{},
	}
}

// workerError is the Worker's error body.
type workerError struct {
	Error   string `json:"error"`
	Message string `json:"message"`
	Field   string `json:"field"`
}

// Price sends req to the Worker and returns its quote, or a
// gRPC status error describing why it could not be priced.
func (c *HTTPClient) Price(ctx context.Context, req Request) (*Quote, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	body, err := json.Marshal(req)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "pricing request could not be encoded: %v", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, status.Errorf(codes.Internal, "pricing endpoint %q is not a valid URL", c.endpoint)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || ctx.Err() == context.DeadlineExceeded {
			return nil, status.Errorf(codes.DeadlineExceeded,
				"pricing service did not respond within %s", c.timeout)
		}
		if errors.Is(err, context.Canceled) {
			return nil, status.Error(codes.Canceled, "pricing request was cancelled")
		}
		return nil, status.Errorf(codes.Unavailable, "pricing service is unreachable: %v", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, status.Errorf(codes.DeadlineExceeded,
				"pricing service did not respond within %s", c.timeout)
		}
		return nil, status.Errorf(codes.Unavailable, "pricing response was cut off: %v", err)
	}

	if resp.StatusCode == http.StatusOK {
		var quote Quote
		if err := json.Unmarshal(raw, &quote); err != nil {
			return nil, status.Error(codes.Internal, "pricing service returned a response that is not valid JSON")
		}
		if err := quote.validate(req); err != nil {
			return nil, status.Errorf(codes.Internal, "pricing service returned an unusable quote: %v", err)
		}
		return &quote, nil
	}

	return nil, errorFor(resp.StatusCode, raw)
}

// errorFor maps a non-200 Worker response onto a gRPC status.
func errorFor(httpStatus int, raw []byte) error {
	var werr workerError
	_ = json.Unmarshal(raw, &werr)
	detail := werr.Message
	if detail == "" {
		detail = strings.TrimSpace(http.StatusText(httpStatus))
	}

	switch {
	case httpStatus == http.StatusBadRequest:
		return status.Errorf(codes.InvalidArgument, "pricing rejected the order: %s", detail)
	case httpStatus == http.StatusUnprocessableEntity:
		return status.Errorf(codes.FailedPrecondition, "the order cannot be priced: %s", detail)
	case httpStatus == http.StatusUnauthorized || httpStatus == http.StatusForbidden:
		// The customer did nothing wrong here: the Order
		// Service's own credential was refused. This is
		// reported as PermissionDenied rather than
		// Unauthenticated so the storefront does not mistake it
		// for the customer's session having expired.
		return status.Errorf(codes.PermissionDenied,
			"pricing service refused the Order Service's credentials (HTTP %d)", httpStatus)
	case httpStatus == http.StatusNotFound || httpStatus == http.StatusMethodNotAllowed:
		return status.Errorf(codes.Unavailable,
			"pricing service is not deployed at the configured address (HTTP %d)", httpStatus)
	case httpStatus == http.StatusTooManyRequests || httpStatus >= 500:
		return status.Errorf(codes.Unavailable, "pricing service failed: HTTP %d %s", httpStatus, detail)
	default:
		return status.Errorf(codes.Internal, "pricing service answered unexpectedly: HTTP %d %s", httpStatus, detail)
	}
}

// validate guards against a 200 whose body does not actually
// describe this order, so a misbehaving function can never cause
// a customer to be charged a wrong or nonsensical amount.
func (q *Quote) validate(req Request) error {
	switch {
	case q.ProductID != req.ProductID:
		return fmt.Errorf("quote is for product %q, not %q", q.ProductID, req.ProductID)
	case q.Quantity != req.Quantity:
		return fmt.Errorf("quote is for quantity %d, not %d", q.Quantity, req.Quantity)
	case q.Total <= 0:
		return fmt.Errorf("total %.2f is not positive", q.Total)
	case q.Discount < 0 || q.DeliveryFee < 0 || q.Subtotal <= 0:
		return errors.New("negative or missing amounts")
	}
	// The parts must add up to the total, to the cent.
	if diff := q.Subtotal - q.Discount + q.DeliveryFee - q.Total; diff > 0.005 || diff < -0.005 {
		return fmt.Errorf("subtotal %.2f - discount %.2f + delivery %.2f does not equal total %.2f",
			q.Subtotal, q.Discount, q.DeliveryFee, q.Total)
	}
	return nil
}

// Unconfigured is used when the Order Service starts without a
// pricing endpoint. It refuses every call, so an order is never
// charged a price the pricing function did not produce.
type Unconfigured struct{}

func (Unconfigured) Price(context.Context, Request) (*Quote, error) {
	return nil, status.Error(codes.Unavailable, "pricing service is not configured (PRICING_URL is unset)")
}

// Flat prices at unit price × quantity with no discount or
// delivery. It stands in for the Worker in tests whose subject
// is not pricing.
type Flat struct{}

func (Flat) Price(_ context.Context, req Request) (*Quote, error) {
	subtotal := req.UnitPrice * float64(req.Quantity)
	return &Quote{
		ProductID: req.ProductID, Quantity: req.Quantity, UnitPrice: req.UnitPrice,
		Subtotal: subtotal, Total: subtotal, Currency: "BTN", PricingVersion: "flat",
	}, nil
}
