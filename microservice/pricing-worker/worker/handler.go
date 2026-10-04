// Package worker is the HTTP face of the calculate-order-price
// Worker: routing, authentication and the JSON contract around
// the pricing rules. It has no Cloudflare dependency, so the same
// handler runs on Cloudflare (via main.go), under `go test`, and
// inside the main application's integration tests.
package worker

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"calculate-order-price/pricing"
)

// maxBodyBytes bounds the request body; a pricing request is a
// few dozen bytes.
const maxBodyBytes = 4 << 10

// handler serves POST /price. getenv reads the Worker's
// variables and secrets, so the same handler runs on Cloudflare
// and under `go test`.
type handler struct {
	getenv func(string) string
}

// NewHandler returns the Worker's HTTP handler.
func NewHandler(getenv func(string) string) http.Handler {
	return &handler{getenv: getenv}
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/price" {
		writeError(w, &pricing.Error{Status: 404, Code: "NOT_FOUND", Message: "no route for " + r.URL.Path + "; use POST /price"})
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeError(w, &pricing.Error{Status: 405, Code: "METHOD_NOT_ALLOWED", Message: "use POST /price"})
		return
	}
	if perr := h.authorize(r); perr != nil {
		writeError(w, perr)
		return
	}

	// SIMULATED_DELAY_MS is set in the Worker's own settings,
	// never by the caller, to demonstrate the Order Service's
	// timeout handling. It is unset in normal operation.
	if ms, err := strconv.Atoi(h.getenv("SIMULATED_DELAY_MS")); err == nil && ms > 0 {
		time.Sleep(time.Duration(ms) * time.Millisecond)
	}

	var req pricing.Request
	dec := json.NewDecoder(io.LimitReader(r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, decodeError(err))
		return
	}

	result, perr := pricing.Calculate(req)
	if perr != nil {
		writeError(w, perr)
		return
	}

	log.Printf(`{"level":"INFO","status":200,"product_id":%q,"quantity":%d,"total":%.2f}`,
		result.ProductID, result.Quantity, result.Total)
	writeJSON(w, http.StatusOK, result)
}

// authorize checks the bearer token against the
// PRICING_API_TOKEN secret.
func (h *handler) authorize(r *http.Request) *pricing.Error {
	want := h.getenv("PRICING_API_TOKEN")
	if want == "" {
		// Fail closed: a Worker deployed without its secret
		// refuses every call rather than serving them openly.
		return &pricing.Error{Status: 500, Code: "MISCONFIGURED", Message: "pricing service has no API token configured"}
	}
	got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
		return &pricing.Error{Status: 401, Code: "UNAUTHORIZED", Message: "missing or invalid API token"}
	}
	return nil
}

// decodeError turns a JSON decoding failure into a field-level
// INVALID_INPUT where it can.
func decodeError(err error) *pricing.Error {
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) && typeErr.Field != "" {
		return &pricing.Error{Status: 400, Code: "INVALID_INPUT", Field: typeErr.Field,
			Message: typeErr.Field + " must be a " + jsonKind(typeErr.Type.Kind().String())}
	}
	if errors.Is(err, io.EOF) {
		return &pricing.Error{Status: 400, Code: "INVALID_INPUT", Field: "body", Message: "request body is empty"}
	}
	if field, ok := strings.CutPrefix(err.Error(), "json: unknown field "); ok {
		field = strings.Trim(field, `"`)
		return &pricing.Error{Status: 400, Code: "INVALID_INPUT", Field: field, Message: "unknown field " + field}
	}
	return &pricing.Error{Status: 400, Code: "INVALID_INPUT", Field: "body", Message: "request body must be a JSON object"}
}

func jsonKind(goKind string) string {
	switch goKind {
	case "float64":
		return "number"
	case "string":
		return "string"
	}
	return goKind
}

func writeError(w http.ResponseWriter, perr *pricing.Error) {
	log.Printf(`{"level":"WARN","status":%d,"error":%q,"message":%q}`, perr.Status, perr.Code, perr.Message)
	writeJSON(w, perr.Status, perr)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
