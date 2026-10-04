package main

import (
	"embed"
	"encoding/json"
	"io/fs"
	"log"
	"net/http"
	"os"
	"time"

	pb "ecommerce-microservices/proto"

	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
)

// staticAssets holds the single-page UI, compiled into the
// binary so the gateway can be started on its own with no
// extra files to copy around.
//
//go:embed static
var staticAssets embed.FS

// uiFiles is staticAssets rooted at the static/ directory, so
// index.html is served from "/" rather than "/static/".
var uiFiles fs.FS

func init() {
	sub, err := fs.Sub(staticAssets, "static")
	if err != nil {
		log.Fatalf("failed to mount embedded UI: %v", err)
	}
	uiFiles = sub
}

// dial opens a plaintext gRPC connection to one of the
// backing services. grpc.NewClient is lazy, so a service that
// is currently stopped does not prevent the gateway from
// starting — calls to it simply fail with Unavailable, which
// is exactly the behaviour Part D scenario 5 exercises.
func dial(target, name string) *grpc.ClientConn {
	conn, err := grpc.NewClient(target,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		log.Fatalf("failed to create client for %s at %s: %v", name, target, err)
	}
	return conn
}

// envOr returns the value of the named environment variable,
// or fallback when it is unset.
func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

// healthHandler reports the connectivity state of each
// upstream connection, so the UI can show at a glance which
// services are reachable.
func healthHandler(conns map[string]*grpc.ClientConn) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		statuses := make(map[string]string, len(conns))
		for name, conn := range conns {
			state := conn.GetState()
			// An idle connection has not been used yet; ask it
			// to connect so the reported state is meaningful.
			if state == connectivity.Idle {
				conn.Connect()
				state = conn.GetState()
			}
			statuses[name] = state.String()
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(statuses)
	}
}

func main() {

	productConn := dial(envOr("PRODUCT_ADDR", "localhost:50051"), "Product Service")
	defer productConn.Close()

	customerConn := dial(envOr("CUSTOMER_ADDR", "localhost:50052"), "Customer Service")
	defer customerConn.Close()

	inventoryConn := dial(envOr("INVENTORY_ADDR", "localhost:50053"), "Inventory Service")
	defer inventoryConn.Close()

	orderConn := dial(envOr("ORDER_ADDR", "localhost:50056"), "Order Service")
	defer orderConn.Close()

	// Payment and Notification are called by the Order Service,
	// not by the gateway. These connections exist only so the
	// UI's status strip can report on every service in the
	// system — including the ones a failing order actually
	// depends on.
	paymentConn := dial(envOr("PAYMENT_ADDR", "localhost:50054"), "Payment Service")
	defer paymentConn.Close()

	notificationConn := dial(envOr("NOTIFICATION_ADDR", "localhost:50055"), "Notification Service")
	defer notificationConn.Close()

	gateway := NewGateway(
		pb.NewProductServiceClient(productConn),
		pb.NewCustomerServiceClient(customerConn),
		pb.NewInventoryServiceClient(inventoryConn),
		pb.NewOrderServiceClient(orderConn),
		5*time.Second,
	)

	gateway.Health = healthHandler(map[string]*grpc.ClientConn{
		"product":      productConn,
		"customer":     customerConn,
		"inventory":    inventoryConn,
		"payment":      paymentConn,
		"notification": notificationConn,
		"order":        orderConn,
	})

	// 8080 is deliberately avoided: another process on this
	// machine already listens there, and under WSL's mirrored
	// networking mode a Windows listener blocks the same port
	// inside Linux. Override with GATEWAY_ADDR if needed.
	addr := envOr("GATEWAY_ADDR", ":8081")

	server := &http.Server{
		Addr:    addr,
		Handler: gateway.Routes(),
		// Generous enough to outlast the Order Service's own
		// fan-out, so a slow dependency surfaces as a gateway
		// timeout message rather than a dropped connection.
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      30 * time.Second,
	}

	log.Printf("Gateway UI is running on http://localhost%s ...", addr)

	if err := server.ListenAndServe(); err != nil {
		log.Fatalf("gateway server stopped: %v", err)
	}
}
