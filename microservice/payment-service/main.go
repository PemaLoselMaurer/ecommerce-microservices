package main

import (
	"log"
	"net"
	"os"

	pb "ecommerce-microservices/proto"

	"ecommerce-microservices/internal/payment"

	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

func listenAddress() string {
	if addr := os.Getenv("PAYMENT_LISTEN"); addr != "" {
		return addr
	}
	return ":50054"
}

func main() {

	delay := payment.GatewayDelay()
	log.Printf("simulated payment gateway delay: %s", delay)

	paymentServer := payment.NewServer(delay)

	addr := listenAddress()

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("failed to listen on %s: %v", addr, err)
	}

	grpcServer := grpc.NewServer()

	pb.RegisterPaymentServiceServer(grpcServer, paymentServer)
	reflection.Register(grpcServer)

	log.Printf("Payment Service is running on %s...", addr)

	if err := grpcServer.Serve(listener); err != nil {
		log.Fatalf("failed to start gRPC server: %v", err)
	}
}
