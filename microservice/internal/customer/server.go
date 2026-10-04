// Package customer holds the Customer Service implementation,
// which owns customer identity and is therefore the only
// service that ever sees a password.
package customer

import (
	"context"
	"fmt"
	"log"
	"net/mail"
	"strings"
	"sync"

	pb "ecommerce-microservices/proto"

	"golang.org/x/crypto/bcrypt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// MinPasswordLength is the shortest password the service will
// accept when registering an account.
const MinPasswordLength = 8

// HashCost is the bcrypt work factor. It is deliberately
// expensive in production — that cost is what makes a stolen
// hash impractical to crack — but the tests lower it, so a
// suite that registers dozens of accounts does not spend all
// its time hashing.
var HashCost = bcrypt.DefaultCost

// account pairs a customer record with the credential used to
// sign in as them. The credential never leaves this service:
// GetCustomer, CreateCustomer and Authenticate all return a
// pb.Customer, which has no password field.
type account struct {
	customer     *pb.Customer
	passwordHash []byte
}

// Server implements the CustomerService defined in
// customer.proto.
type Server struct {
	pb.UnimplementedCustomerServiceServer

	mu       sync.Mutex
	accounts map[string]*account // keyed by customer ID
	byEmail  map[string]string   // normalised email -> customer ID
	nextID   int
}

// NewServer wires a Server over a set of accounts, building
// the email index from them.
func NewServer(accounts map[string]*account) *Server {
	byEmail := make(map[string]string, len(accounts))
	for id, acct := range accounts {
		byEmail[normaliseEmail(acct.customer.GetEmail())] = id
	}

	return &Server{
		accounts: accounts,
		byEmail:  byEmail,
		nextID:   len(accounts),
	}
}

// normaliseEmail lower-cases and trims an address so that
// "Alice@Example.com " and "alice@example.com" are the same
// account.
func normaliseEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// GetCustomer receives a customer ID and returns the
// corresponding customer information.
func (s *Server) GetCustomer(
	ctx context.Context,
	req *pb.GetCustomerRequest,
) (*pb.Customer, error) {

	if req.GetCustomerId() == "" {
		return nil, status.Error(
			codes.InvalidArgument,
			"customer ID is required",
		)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	acct, exists := s.accounts[req.GetCustomerId()]
	if !exists {
		return nil, status.Errorf(
			codes.NotFound,
			"customer with ID %s not found",
			req.GetCustomerId(),
		)
	}

	return acct.customer, nil
}

// CreateCustomer registers a new customer and returns the
// stored record, including its generated customer ID. The
// password is hashed with bcrypt before it is stored, so the
// service never holds a password it could leak.
func (s *Server) CreateCustomer(
	ctx context.Context,
	req *pb.CreateCustomerRequest,
) (*pb.Customer, error) {

	name := strings.TrimSpace(req.GetName())
	email := normaliseEmail(req.GetEmail())

	if name == "" || email == "" {
		return nil, status.Error(
			codes.InvalidArgument,
			"name and email are required",
		)
	}
	if _, err := mail.ParseAddress(email); err != nil {
		return nil, status.Errorf(
			codes.InvalidArgument,
			"%q is not a valid email address",
			req.GetEmail(),
		)
	}
	if len(req.GetPassword()) < MinPasswordLength {
		return nil, status.Errorf(
			codes.InvalidArgument,
			"password must be at least %d characters",
			MinPasswordLength,
		)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.GetPassword()), HashCost)
	if err != nil {
		return nil, status.Error(codes.Internal, "could not secure the password")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// An email identifies an account, so it has to be unique.
	if _, taken := s.byEmail[email]; taken {
		return nil, status.Errorf(
			codes.AlreadyExists,
			"an account already exists for %s",
			email,
		)
	}

	s.nextID++
	customer := &pb.Customer{
		CustomerId: fmt.Sprintf("C%03d", s.nextID),
		Name:       name,
		Email:      email,
		Address:    strings.TrimSpace(req.GetAddress()),
	}

	s.accounts[customer.CustomerId] = &account{customer: customer, passwordHash: hash}
	s.byEmail[email] = customer.CustomerId

	log.Printf("registered customer %s (%s)", customer.CustomerId, email)

	return customer, nil
}

// Authenticate verifies an email and password and returns the
// customer they belong to.
//
// A wrong email and a wrong password produce the same
// Unauthenticated error with the same wording, so the response
// cannot be used to discover which addresses have accounts.
func (s *Server) Authenticate(
	ctx context.Context,
	req *pb.AuthenticateRequest,
) (*pb.Customer, error) {

	email := normaliseEmail(req.GetEmail())

	if email == "" || req.GetPassword() == "" {
		return nil, status.Error(
			codes.InvalidArgument,
			"email and password are required",
		)
	}

	s.mu.Lock()
	customerID, exists := s.byEmail[email]
	var acct *account
	if exists {
		acct = s.accounts[customerID]
	}
	s.mu.Unlock()

	if acct == nil {
		// Hash a throwaway value anyway so that an unknown email
		// takes about as long to reject as a wrong password,
		// leaving no timing signal to enumerate accounts.
		_ = bcrypt.CompareHashAndPassword(
			[]byte("$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy"),
			[]byte(req.GetPassword()),
		)
		return nil, status.Error(codes.Unauthenticated, "incorrect email or password")
	}

	if err := bcrypt.CompareHashAndPassword(acct.passwordHash, []byte(req.GetPassword())); err != nil {
		log.Printf("failed sign-in attempt for %s", email)
		return nil, status.Error(codes.Unauthenticated, "incorrect email or password")
	}

	log.Printf("customer %s signed in", acct.customer.GetCustomerId())

	return acct.customer, nil
}

// NewAccount builds an account with a known password, for
// seeding and for tests.
func NewAccount(id, name, email, address, password string) *account {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), HashCost)
	if err != nil {
		log.Fatalf("failed to build account %s: %v", id, err)
	}

	return &account{
		customer: &pb.Customer{
			CustomerId: id,
			Name:       name,
			Email:      email,
			Address:    address,
		},
		passwordHash: hash,
	}
}

// SeedAccounts returns the customers held in memory. The demo
// passwords are shown on the storefront's sign-in screen so
// the system can be exercised without registering first.
func SeedAccounts() map[string]*account {
	return map[string]*account{
		"C001": NewAccount("C001", "Alice Nguyen", "alice@example.com", "123 Market St, Thimphu", "alice1234"),
		"C002": NewAccount("C002", "Bob Tan", "bob@example.com", "456 Orchard Rd, Paro", "bob12345"),
	}
}
