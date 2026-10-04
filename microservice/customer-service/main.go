package main

import (
	"log"
	"net"
	"os"

	pb "ecommerce-microservices/proto"

	"ecommerce-microservices/internal/customer"

	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

func listenAddress() string {
	if addr := os.Getenv("CUSTOMER_LISTEN"); addr != "" {
		return addr
	}
	return ":50052"
}

func main() {

	accounts := customer.SeedAccounts()
	customerServer := customer.NewServer(accounts)

	addr := listenAddress()

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("failed to listen on %s: %v", addr, err)
	}

	// Create a gRPC server.
	grpcServer := grpc.NewServer()

	// Register the Customer Service.
	pb.RegisterCustomerServiceServer(grpcServer, customerServer)

	// Register reflection so tools like grpcurl can
	// discover and describe the service at runtime.
	reflection.Register(grpcServer)

	log.Printf("Customer Service is running on %s with %d accounts...", addr, len(accounts))

	// Start the gRPC server.
	if err := grpcServer.Serve(listener); err != nil {
		log.Fatalf("failed to start gRPC server: %v", err)
	}
}
