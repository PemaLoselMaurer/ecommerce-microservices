package integration

import (
	"net/http/httptest"
	"testing"
	"time"

	"ecommerce-microservices/internal/pricing"

	"calculate-order-price/worker"
)

// pricingTestToken is the bearer token the in-test Worker
// expects. It exists only inside the test process.
const pricingTestToken = "integration-test-token"

// pricingWorker is the real calculate-order-price Worker handler
// — the same code deployed to Cloudflare — served over real HTTP
// on a loopback port. The Order Service reaches it through the
// same HTTP client it uses in production.
type pricingWorker struct {
	server *httptest.Server
	vars   map[string]string
}

func startPricing(t *testing.T) *pricingWorker {
	t.Helper()

	w := &pricingWorker{vars: map[string]string{"PRICING_API_TOKEN": pricingTestToken}}
	w.server = httptest.NewServer(worker.NewHandler(func(name string) string { return w.vars[name] }))
	t.Cleanup(w.server.Close)
	return w
}

// client returns the production HTTP client pointed at this
// Worker with the correct token.
func (w *pricingWorker) client() pricing.Client {
	return pricing.NewHTTPClient(w.server.URL, pricingTestToken, 2*time.Second)
}
