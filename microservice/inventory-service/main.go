package main

import (
	"log"
	"net"
	"os"

	pb "ecommerce-microservices/proto"

	"ecommerce-microservices/internal/inventory"

	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

func listenAddress() string {
	if addr := os.Getenv("INVENTORY_LISTEN"); addr != "" {
		return addr
	}
	return ":50053"
}

func main() {

	// Simulated flakiness is on by default so the Retry demo
	// from the previous practical still works; set
	// INVENTORY_FLAKY=false to turn it off.
	flaky := os.Getenv("INVENTORY_FLAKY") != "false"
	log.Printf("simulated transient ReserveStock failures: %t", flaky)

	inventoryServer := inventory.NewServer(inventory.InitialStock(), flaky)

	addr := listenAddress()

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("failed to listen on %s: %v", addr, err)
	}

	grpcServer := grpc.NewServer()

	pb.RegisterInventoryServiceServer(grpcServer, inventoryServer)
	reflection.Register(grpcServer)

	log.Printf("Inventory Service is running on %s...", addr)

	if err := grpcServer.Serve(listener); err != nil {
		log.Fatalf("failed to start gRPC server: %v", err)
	}
}
