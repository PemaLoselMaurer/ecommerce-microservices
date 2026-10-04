// Command calculate-order-price is a Cloudflare Worker, written
// in Go and compiled to WebAssembly, that prices an order for
// the Order Service. See pricing/ for the rules and worker/ for
// the HTTP contract.
package main

import (
	"calculate-order-price/worker"

	"github.com/syumai/workers-go"
)

func main() {
	workers.Serve(worker.NewHandler(getenv))
}
