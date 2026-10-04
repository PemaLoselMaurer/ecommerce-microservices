// Package product holds the Product Service implementation.
//
// It lives here rather than in product-service/main.go so that
// the integration tests can stand a real Product Service up
// over a real gRPC connection. A package main cannot be
// imported, so an implementation that stays there can only
// ever be tested through a network port.
package product

import (
	"context"
	"sort"
	"strings"

	pb "ecommerce-microservices/proto"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Server implements the ProductService defined in
// product.proto.
type Server struct {
	pb.UnimplementedProductServiceServer

	products map[string]*pb.Product
}

// NewServer builds a Product Service over the given catalogue.
func NewServer(products map[string]*pb.Product) *Server {
	return &Server{products: products}
}

// GetProduct receives a product ID and returns the
// corresponding product information.
func (s *Server) GetProduct(
	ctx context.Context,
	req *pb.GetProductRequest,
) (*pb.Product, error) {

	// Check whether the product ID was provided.
	if req.GetProductId() == "" {
		return nil, status.Error(
			codes.InvalidArgument,
			"product ID is required",
		)
	}

	// Search for the product.
	product, exists := s.products[req.GetProductId()]

	// If the product does not exist, return NOT_FOUND.
	if !exists {
		return nil, status.Errorf(
			codes.NotFound,
			"product with ID %s not found",
			req.GetProductId(),
		)
	}

	// Return the product information.
	return product, nil
}

// ListProducts streams the catalogue in product ID order,
// optionally narrowed to one category. An unknown category is
// not an error — it simply yields an empty catalogue, which
// the storefront renders as "no products found".
func (s *Server) ListProducts(
	req *pb.ListProductsRequest,
	stream pb.ProductService_ListProductsServer,
) error {

	category := strings.ToLower(strings.TrimSpace(req.GetCategory()))

	ids := make([]string, 0, len(s.products))
	for id := range s.products {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	for _, id := range ids {
		product := s.products[id]

		if category != "" && strings.ToLower(product.GetCategory()) != category {
			continue
		}

		if err := stream.Send(product); err != nil {
			return err
		}
	}

	return nil
}

// Catalogue returns the product information held in memory.
// ListPrice above Price marks a product as discounted; the
// storefront works out the saving from the two.
//
// It is a function rather than a literal inside main so the
// tests exercise exactly the catalogue the service runs with,
// instead of a copy that can drift out of step.
func Catalogue() map[string]*pb.Product {
	return map[string]*pb.Product{
		"P001": {
			ProductId:   "P001",
			Name:        "Laptop",
			Price:       75000,
			ListPrice:   88000,
			Badge:       "BESTSELLER",
			Rating:      4.6,
			ReviewCount: 214,
			Category:    "laptops",
			Description: "14-inch ultrabook with a 12-core processor, 16 GB of memory and a 512 GB SSD. Weighs 1.2 kg and holds a full working day on one charge.",
		},
		"P002": {
			ProductId:   "P002",
			Name:        "Mechanical Keyboard",
			Price:       4500,
			Rating:      4.4,
			ReviewCount: 96,
			Category:    "accessories",
			Description: "Tenkeyless board with hot-swappable tactile switches, PBT keycaps and per-key backlighting. Detachable braided cable.",
		},
		"P003": {
			ProductId:   "P003",
			Name:        "Wireless Mouse",
			Price:       1800,
			ListPrice:   2400,
			Rating:      4.2,
			ReviewCount: 158,
			Category:    "accessories",
			Description: "Lightweight 2.4 GHz mouse with a 16,000 DPI sensor, six programmable buttons and up to 70 hours of use per charge.",
		},
		"P004": {
			ProductId:   "P004",
			Name:        "Monitor",
			Price:       25000,
			Rating:      4.7,
			ReviewCount: 73,
			Category:    "displays",
			Description: "27-inch QHD IPS panel at 165 Hz with 1 ms response, 99% sRGB coverage and a fully adjustable stand.",
		},
		"P005": {
			ProductId:   "P005",
			Name:        "Noise-Cancelling Headphones",
			Price:       12000,
			ListPrice:   15000,
			Badge:       "DEAL OF THE WEEK",
			Rating:      4.5,
			ReviewCount: 402,
			Category:    "audio",
			Description: "Over-ear headphones with adaptive noise cancelling, 40 mm drivers and 30 hours of battery. Folds flat for travel.",
		},
		"P006": {
			ProductId:   "P006",
			Name:        "Smartphone",
			Price:       48000,
			Badge:       "NEW",
			Rating:      4.3,
			ReviewCount: 51,
			Category:    "mobile",
			Description: "6.5-inch OLED display, triple camera system with optical stabilisation, 256 GB of storage and 5G.",
		},
		"P007": {
			ProductId:   "P007",
			Name:        "Tablet",
			Price:       32000,
			ListPrice:   36000,
			Badge:       "NEW",
			Rating:      4.1,
			ReviewCount: 28,
			Category:    "mobile",
			Description: "11-inch 120 Hz tablet with pen support, quad speakers and 128 GB of storage. Good for notes and sketching.",
		},
		"P008": {
			ProductId:   "P008",
			Name:        "Webcam",
			Price:       6500,
			Rating:      3.9,
			ReviewCount: 64,
			Category:    "accessories",
			Description: "1440p webcam with autofocus, dual noise-cancelling microphones and a physical privacy shutter.",
		},
	}
}
