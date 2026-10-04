package main

import (
	"log"
	"net"
	"os"

	pb "ecommerce-microservices/proto"

	"ecommerce-microservices/internal/product"

	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

// listenAddress returns the address to serve on, overridable
// so the service can be started somewhere else when its usual
// port is taken.
func listenAddress() string {
	if addr := os.Getenv("PRODUCT_LISTEN"); addr != "" {
		return addr
	}
	return ":50051"
}

func main() {

	products := product.Catalogue()

	// Create the Product Service server.
	productServer := product.NewServer(products)

	addr := listenAddress()

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("failed to listen on %s: %v", addr, err)
	}

	// Create a gRPC server.
	grpcServer := grpc.NewServer()

	// Register the Product Service.
	pb.RegisterProductServiceServer(grpcServer, productServer)

	// Register reflection so tools like grpcurl can
	// discover and describe the service at runtime.
	reflection.Register(grpcServer)

	log.Printf("Product Service is running on %s with %d products...", addr, len(products))

	// Start the gRPC server.
	if err := grpcServer.Serve(listener); err != nil {
		log.Fatalf("failed to start gRPC server: %v", err)
	}
}
