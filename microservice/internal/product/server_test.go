package product

import (
	"context"
	"strings"
	"testing"

	pb "ecommerce-microservices/proto"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// These tests exercise ProductServiceServer directly, with no
// gRPC server listening and no other service running. They are
// unit tests in the strict sense: one component, in process.

// newTestServer builds a server over the real catalogue, so
// the tests assert against the data the service actually
// serves rather than a fixture that can drift.
func newTestServer() *Server {
	return NewServer(Catalogue())
}

// assertCode fails the test unless err carries the expected
// gRPC status code.
func assertCode(t *testing.T, err error, want codes.Code) {
	t.Helper()

	if err == nil {
		t.Fatalf("expected error with code %s, got nil", want)
	}

	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected a gRPC status error, got %T: %v", err, err)
	}
	if st.Code() != want {
		t.Fatalf("expected code %s, got %s (%q)", want, st.Code(), st.Message())
	}
}

// ---------- GetProduct: the success path ----------

func TestGetProductReturnsExistingProduct(t *testing.T) {
	server := newTestServer()

	product, err := server.GetProduct(context.Background(), &pb.GetProductRequest{ProductId: "P001"})
	if err != nil {
		t.Fatalf("GetProduct(P001) returned an unexpected error: %v", err)
	}

	// Every field is checked, not just that something came
	// back, so a mix-up between records would be caught.
	if product.GetProductId() != "P001" {
		t.Errorf("product ID: got %q, want %q", product.GetProductId(), "P001")
	}
	if product.GetName() != "Laptop" {
		t.Errorf("name: got %q, want %q", product.GetName(), "Laptop")
	}
	if product.GetPrice() != 75000 {
		t.Errorf("price: got %v, want %v", product.GetPrice(), 75000.0)
	}
	if product.GetCategory() != "laptops" {
		t.Errorf("category: got %q, want %q", product.GetCategory(), "laptops")
	}
	if product.GetListPrice() != 88000 {
		t.Errorf("list price: got %v, want %v", product.GetListPrice(), 88000.0)
	}
	if product.GetDescription() == "" {
		t.Error("description: got an empty string, want the product description")
	}
}

// ---------- GetProduct: different identifiers ----------

func TestGetProductReturnsTheRecordMatchingEachID(t *testing.T) {
	server := newTestServer()

	cases := []struct {
		productID string
		wantName  string
		wantPrice float64
	}{
		{"P001", "Laptop", 75000},
		{"P002", "Mechanical Keyboard", 4500},
		{"P003", "Wireless Mouse", 1800},
		{"P004", "Monitor", 25000},
		{"P005", "Noise-Cancelling Headphones", 12000},
		{"P006", "Smartphone", 48000},
		{"P007", "Tablet", 32000},
		{"P008", "Webcam", 6500},
	}

	for _, testCase := range cases {
		t.Run(testCase.productID, func(t *testing.T) {
			product, err := server.GetProduct(context.Background(),
				&pb.GetProductRequest{ProductId: testCase.productID})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if product.GetProductId() != testCase.productID {
				t.Errorf("product ID: got %q, want %q", product.GetProductId(), testCase.productID)
			}
			if product.GetName() != testCase.wantName {
				t.Errorf("name: got %q, want %q", product.GetName(), testCase.wantName)
			}
			if product.GetPrice() != testCase.wantPrice {
				t.Errorf("price: got %v, want %v", product.GetPrice(), testCase.wantPrice)
			}
		})
	}
}

// ---------- GetProduct: failure paths ----------

func TestGetProductRejectsUnknownID(t *testing.T) {
	server := newTestServer()

	cases := []string{"P999", "p001", "ABC", "0", "P001 "}

	for _, productID := range cases {
		t.Run(productID, func(t *testing.T) {
			product, err := server.GetProduct(context.Background(),
				&pb.GetProductRequest{ProductId: productID})

			assertCode(t, err, codes.NotFound)
			if product != nil {
				t.Errorf("expected no product alongside the error, got %v", product)
			}
		})
	}
}

func TestGetProductRejectsEmptyID(t *testing.T) {
	server := newTestServer()

	product, err := server.GetProduct(context.Background(), &pb.GetProductRequest{ProductId: ""})

	// An empty ID is a malformed request, not a missing record,
	// so it must be InvalidArgument rather than NotFound —
	// the gateway maps the two to different HTTP statuses.
	assertCode(t, err, codes.InvalidArgument)
	if product != nil {
		t.Errorf("expected no product alongside the error, got %v", product)
	}
}

func TestGetProductRejectsNilRequest(t *testing.T) {
	server := newTestServer()

	// The generated getters are nil-safe, so a nil request
	// should be handled like an empty one rather than panic.
	_, err := server.GetProduct(context.Background(), nil)

	assertCode(t, err, codes.InvalidArgument)
}

func TestGetProductErrorMessageNamesTheID(t *testing.T) {
	server := newTestServer()

	_, err := server.GetProduct(context.Background(), &pb.GetProductRequest{ProductId: "P999"})

	st, _ := status.FromError(err)
	if !strings.Contains(st.Message(), "P999") {
		t.Errorf("error message %q does not mention the requested ID", st.Message())
	}
}

// ---------- ListProducts ----------

// recordingStream captures everything ListProducts sends,
// standing in for a real gRPC server stream.
type recordingStream struct {
	grpc.ServerStream
	ctx  context.Context
	sent []*pb.Product
	err  error // when set, Send fails with it
}

func (s *recordingStream) Send(product *pb.Product) error {
	if s.err != nil {
		return s.err
	}
	s.sent = append(s.sent, product)
	return nil
}

func (s *recordingStream) Context() context.Context {
	if s.ctx != nil {
		return s.ctx
	}
	return context.Background()
}

func TestListProductsStreamsWholeCatalogueInIDOrder(t *testing.T) {
	server := newTestServer()
	stream := &recordingStream{}

	if err := server.ListProducts(&pb.ListProductsRequest{}, stream); err != nil {
		t.Fatalf("ListProducts returned an unexpected error: %v", err)
	}

	if len(stream.sent) != len(Catalogue()) {
		t.Fatalf("streamed %d products, want %d", len(stream.sent), len(Catalogue()))
	}

	// Ordering is part of the contract: the storefront relies
	// on it to render a stable grid.
	want := []string{"P001", "P002", "P003", "P004", "P005", "P006", "P007", "P008"}
	for i, expected := range want {
		if stream.sent[i].GetProductId() != expected {
			t.Errorf("position %d: got %q, want %q", i, stream.sent[i].GetProductId(), expected)
		}
	}
}

func TestListProductsFiltersByCategory(t *testing.T) {
	cases := []struct {
		category string
		wantIDs  []string
	}{
		{"laptops", []string{"P001"}},
		{"displays", []string{"P004"}},
		{"audio", []string{"P005"}},
		{"mobile", []string{"P006", "P007"}},
		{"accessories", []string{"P002", "P003", "P008"}},

		// Matching is case-insensitive and ignores surrounding
		// whitespace, so a category typed into the URL still works.
		{"MOBILE", []string{"P006", "P007"}},
		{"  audio  ", []string{"P005"}},
	}

	for _, testCase := range cases {
		t.Run(testCase.category, func(t *testing.T) {
			server := newTestServer()
			stream := &recordingStream{}

			if err := server.ListProducts(&pb.ListProductsRequest{Category: testCase.category}, stream); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if len(stream.sent) != len(testCase.wantIDs) {
				t.Fatalf("streamed %d products, want %d", len(stream.sent), len(testCase.wantIDs))
			}
			for i, expected := range testCase.wantIDs {
				if stream.sent[i].GetProductId() != expected {
					t.Errorf("position %d: got %q, want %q", i, stream.sent[i].GetProductId(), expected)
				}
			}
		})
	}
}

func TestListProductsReturnsNothingForUnknownCategory(t *testing.T) {
	server := newTestServer()
	stream := &recordingStream{}

	// An unknown category is an empty result, not an error: the
	// storefront shows "no matching products" rather than a
	// failure banner.
	if err := server.ListProducts(&pb.ListProductsRequest{Category: "furniture"}, stream); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(stream.sent) != 0 {
		t.Errorf("streamed %d products, want 0", len(stream.sent))
	}
}

func TestListProductsPropagatesSendFailure(t *testing.T) {
	server := newTestServer()

	// A client that disconnects mid-stream makes Send fail; the
	// error has to reach the caller rather than be swallowed.
	sendErr := status.Error(codes.Canceled, "client went away")
	stream := &recordingStream{err: sendErr}

	err := server.ListProducts(&pb.ListProductsRequest{}, stream)

	assertCode(t, err, codes.Canceled)
}
