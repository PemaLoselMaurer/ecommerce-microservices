package main

import (
	"log"
	"net"
	"os"

	pb "ecommerce-microservices/proto"

	"ecommerce-microservices/internal/notification"

	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

func listenAddress() string {
	if addr := os.Getenv("NOTIFICATION_LISTEN"); addr != "" {
		return addr
	}
	return ":50055"
}

func main() {

	notificationServer := notification.NewServer()

	addr := listenAddress()

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("failed to listen on %s: %v", addr, err)
	}

	grpcServer := grpc.NewServer()

	pb.RegisterNotificationServiceServer(grpcServer, notificationServer)
	reflection.Register(grpcServer)

	log.Printf("Notification Service is running on %s...", addr)

	if err := grpcServer.Serve(listener); err != nil {
		log.Fatalf("failed to start gRPC server: %v", err)
	}
}
