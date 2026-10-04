package customer

import (
	"context"
	"os"
	"strings"
	"testing"

	pb "ecommerce-microservices/proto"

	"golang.org/x/crypto/bcrypt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// These tests exercise CustomerServiceServer directly, in
// process, with no gRPC server listening and no other service
// running.

// TestMain lowers the bcrypt work factor for the whole suite.
// Hashing at production cost is the point in production, but
// here it would add half a minute of pure CPU to a run that
// otherwise takes a second, without testing anything extra —
// TestCreateCustomerStoresAHashedPassword still proves the
// hashing itself is real.
func TestMain(m *testing.M) {
	HashCost = bcrypt.MinCost
	os.Exit(m.Run())
}

// newTestServer builds a server over the real seed accounts.
// Each test gets its own copy, so a test that registers a
// customer cannot affect another test.
func newTestServer() *Server {
	return NewServer(SeedAccounts())
}

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

// ---------- GetCustomer ----------

func TestGetCustomerReturnsExistingCustomer(t *testing.T) {
	server := newTestServer()

	customer, err := server.GetCustomer(context.Background(),
		&pb.GetCustomerRequest{CustomerId: "C001"})
	if err != nil {
		t.Fatalf("GetCustomer(C001) returned an unexpected error: %v", err)
	}

	if customer.GetCustomerId() != "C001" {
		t.Errorf("customer ID: got %q, want %q", customer.GetCustomerId(), "C001")
	}
	if customer.GetName() != "Alice Nguyen" {
		t.Errorf("name: got %q, want %q", customer.GetName(), "Alice Nguyen")
	}
	if customer.GetEmail() != "alice@example.com" {
		t.Errorf("email: got %q, want %q", customer.GetEmail(), "alice@example.com")
	}
	if customer.GetAddress() == "" {
		t.Error("address: got an empty string, want the stored address")
	}
}

func TestGetCustomerReturnsTheRecordMatchingEachID(t *testing.T) {
	server := newTestServer()

	cases := []struct {
		customerID string
		wantName   string
		wantEmail  string
	}{
		{"C001", "Alice Nguyen", "alice@example.com"},
		{"C002", "Bob Tan", "bob@example.com"},
	}

	for _, testCase := range cases {
		t.Run(testCase.customerID, func(t *testing.T) {
			customer, err := server.GetCustomer(context.Background(),
				&pb.GetCustomerRequest{CustomerId: testCase.customerID})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if customer.GetName() != testCase.wantName {
				t.Errorf("name: got %q, want %q", customer.GetName(), testCase.wantName)
			}
			if customer.GetEmail() != testCase.wantEmail {
				t.Errorf("email: got %q, want %q", customer.GetEmail(), testCase.wantEmail)
			}
		})
	}
}

func TestGetCustomerRejectsUnknownID(t *testing.T) {
	server := newTestServer()

	for _, customerID := range []string{"C999", "c001", "1", "unknown"} {
		t.Run(customerID, func(t *testing.T) {
			customer, err := server.GetCustomer(context.Background(),
				&pb.GetCustomerRequest{CustomerId: customerID})

			assertCode(t, err, codes.NotFound)
			if customer != nil {
				t.Errorf("expected no customer alongside the error, got %v", customer)
			}
		})
	}
}

func TestGetCustomerRejectsEmptyID(t *testing.T) {
	server := newTestServer()

	_, err := server.GetCustomer(context.Background(), &pb.GetCustomerRequest{CustomerId: ""})

	assertCode(t, err, codes.InvalidArgument)
}

// ---------- CreateCustomer ----------

func TestCreateCustomerStoresAndReturnsTheNewRecord(t *testing.T) {
	server := newTestServer()

	customer, err := server.CreateCustomer(context.Background(), &pb.CreateCustomerRequest{
		Name:     "Karma Dorji",
		Email:    "karma@example.com",
		Address:  "Norzin Lam",
		Password: "karma1234",
	})
	if err != nil {
		t.Fatalf("CreateCustomer returned an unexpected error: %v", err)
	}

	// C001 and C002 are seeded, so the next ID is C003.
	if customer.GetCustomerId() != "C003" {
		t.Errorf("customer ID: got %q, want %q", customer.GetCustomerId(), "C003")
	}
	if customer.GetName() != "Karma Dorji" {
		t.Errorf("name: got %q, want %q", customer.GetName(), "Karma Dorji")
	}

	// The record must actually be retrievable afterwards, not
	// just returned once.
	stored, err := server.GetCustomer(context.Background(),
		&pb.GetCustomerRequest{CustomerId: customer.GetCustomerId()})
	if err != nil {
		t.Fatalf("the new customer could not be read back: %v", err)
	}
	if stored.GetEmail() != "karma@example.com" {
		t.Errorf("stored email: got %q, want %q", stored.GetEmail(), "karma@example.com")
	}
}

func TestCreateCustomerNormalisesEmailAndTrimsInput(t *testing.T) {
	server := newTestServer()

	customer, err := server.CreateCustomer(context.Background(), &pb.CreateCustomerRequest{
		Name:     "  Karma Dorji  ",
		Email:    "  KARMA@Example.COM  ",
		Password: "karma1234",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if customer.GetName() != "Karma Dorji" {
		t.Errorf("name was not trimmed: got %q", customer.GetName())
	}
	if customer.GetEmail() != "karma@example.com" {
		t.Errorf("email was not normalised: got %q", customer.GetEmail())
	}
}

func TestCreateCustomerRejectsInvalidInput(t *testing.T) {
	cases := []struct {
		name    string
		request *pb.CreateCustomerRequest
	}{
		{"missing name", &pb.CreateCustomerRequest{Email: "x@example.com", Password: "password1"}},
		{"missing email", &pb.CreateCustomerRequest{Name: "X", Password: "password1"}},
		{"blank name", &pb.CreateCustomerRequest{Name: "   ", Email: "x@example.com", Password: "password1"}},
		{"malformed email", &pb.CreateCustomerRequest{Name: "X", Email: "not-an-email", Password: "password1"}},
		{"short password", &pb.CreateCustomerRequest{Name: "X", Email: "x@example.com", Password: "short"}},
		{"no password", &pb.CreateCustomerRequest{Name: "X", Email: "x@example.com"}},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			server := newTestServer()

			_, err := server.CreateCustomer(context.Background(), testCase.request)

			assertCode(t, err, codes.InvalidArgument)
		})
	}
}

func TestCreateCustomerRejectsDuplicateEmail(t *testing.T) {
	server := newTestServer()

	// Different capitalisation must still count as the same
	// account, or two customers could share one sign-in.
	_, err := server.CreateCustomer(context.Background(), &pb.CreateCustomerRequest{
		Name:     "Impostor",
		Email:    "ALICE@example.com",
		Password: "password1",
	})

	assertCode(t, err, codes.AlreadyExists)
}

func TestCreateCustomerStoresAHashedPassword(t *testing.T) {
	server := newTestServer()

	const plaintext = "karma1234"

	customer, err := server.CreateCustomer(context.Background(), &pb.CreateCustomerRequest{
		Name:     "Karma Dorji",
		Email:    "karma@example.com",
		Password: plaintext,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	stored := server.accounts[customer.GetCustomerId()]

	if string(stored.passwordHash) == plaintext {
		t.Fatal("the password was stored in plain text")
	}
	if !strings.HasPrefix(string(stored.passwordHash), "$2a$") {
		t.Errorf("stored credential is not a bcrypt hash: %q", stored.passwordHash)
	}
	if err := bcrypt.CompareHashAndPassword(stored.passwordHash, []byte(plaintext)); err != nil {
		t.Errorf("the stored hash does not verify against the original password: %v", err)
	}
}

// ---------- Authenticate ----------

func TestAuthenticateAcceptsCorrectCredentials(t *testing.T) {
	server := newTestServer()

	cases := []struct {
		email    string
		password string
		wantID   string
		wantName string
	}{
		{"alice@example.com", "alice1234", "C001", "Alice Nguyen"},
		{"bob@example.com", "bob12345", "C002", "Bob Tan"},

		// Sign-in must survive the capitalisation and stray
		// spaces a browser autofill can introduce.
		{"ALICE@Example.com", "alice1234", "C001", "Alice Nguyen"},
		{"  bob@example.com  ", "bob12345", "C002", "Bob Tan"},
	}

	for _, testCase := range cases {
		t.Run(testCase.email, func(t *testing.T) {
			customer, err := server.Authenticate(context.Background(), &pb.AuthenticateRequest{
				Email:    testCase.email,
				Password: testCase.password,
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if customer.GetCustomerId() != testCase.wantID {
				t.Errorf("customer ID: got %q, want %q", customer.GetCustomerId(), testCase.wantID)
			}
			if customer.GetName() != testCase.wantName {
				t.Errorf("name: got %q, want %q", customer.GetName(), testCase.wantName)
			}
		})
	}
}

func TestAuthenticateRejectsWrongCredentials(t *testing.T) {
	server := newTestServer()

	cases := []struct {
		name     string
		email    string
		password string
	}{
		{"wrong password", "alice@example.com", "wrong-password"},
		{"unknown email", "nobody@example.com", "alice1234"},
		{"password of another account", "alice@example.com", "bob12345"},
		{"password with different case", "alice@example.com", "ALICE1234"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			customer, err := server.Authenticate(context.Background(), &pb.AuthenticateRequest{
				Email:    testCase.email,
				Password: testCase.password,
			})

			assertCode(t, err, codes.Unauthenticated)
			if customer != nil {
				t.Errorf("expected no customer alongside the error, got %v", customer)
			}
		})
	}
}

func TestAuthenticateGivesTheSameAnswerForUnknownEmailAndWrongPassword(t *testing.T) {
	server := newTestServer()

	_, wrongPassword := server.Authenticate(context.Background(), &pb.AuthenticateRequest{
		Email: "alice@example.com", Password: "not-the-password",
	})
	_, unknownEmail := server.Authenticate(context.Background(), &pb.AuthenticateRequest{
		Email: "nobody@example.com", Password: "not-the-password",
	})

	wrongStatus, _ := status.FromError(wrongPassword)
	unknownStatus, _ := status.FromError(unknownEmail)

	// Identical wording is what stops the response being used
	// to discover which addresses have accounts.
	if wrongStatus.Message() != unknownStatus.Message() {
		t.Errorf("the two failures are distinguishable: %q vs %q",
			wrongStatus.Message(), unknownStatus.Message())
	}
	if wrongStatus.Code() != unknownStatus.Code() {
		t.Errorf("the two failures use different codes: %s vs %s",
			wrongStatus.Code(), unknownStatus.Code())
	}
}

func TestAuthenticateRejectsEmptyCredentials(t *testing.T) {
	server := newTestServer()

	cases := []struct {
		name     string
		email    string
		password string
	}{
		{"both empty", "", ""},
		{"empty email", "", "alice1234"},
		{"empty password", "alice@example.com", ""},
		{"whitespace email", "   ", "alice1234"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := server.Authenticate(context.Background(), &pb.AuthenticateRequest{
				Email:    testCase.email,
				Password: testCase.password,
			})

			assertCode(t, err, codes.InvalidArgument)
		})
	}
}

func TestAuthenticateNeverReturnsACredential(t *testing.T) {
	server := newTestServer()

	customer, err := server.Authenticate(context.Background(), &pb.AuthenticateRequest{
		Email: "alice@example.com", Password: "alice1234",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// pb.Customer has no password field, so the check is that
	// nothing password-shaped leaked into the fields it does
	// have.
	for field, value := range map[string]string{
		"name":    customer.GetName(),
		"email":   customer.GetEmail(),
		"address": customer.GetAddress(),
	} {
		if strings.Contains(value, "alice1234") || strings.Contains(value, "$2a$") {
			t.Errorf("%s carries credential material: %q", field, value)
		}
	}
}

func TestRegisteredCustomerCanImmediatelySignIn(t *testing.T) {
	server := newTestServer()

	created, err := server.CreateCustomer(context.Background(), &pb.CreateCustomerRequest{
		Name:     "Karma Dorji",
		Email:    "karma@example.com",
		Password: "karma1234",
	})
	if err != nil {
		t.Fatalf("CreateCustomer failed: %v", err)
	}

	signedIn, err := server.Authenticate(context.Background(), &pb.AuthenticateRequest{
		Email:    "karma@example.com",
		Password: "karma1234",
	})
	if err != nil {
		t.Fatalf("the new customer could not sign in: %v", err)
	}

	if signedIn.GetCustomerId() != created.GetCustomerId() {
		t.Errorf("signed in as %q, want %q", signedIn.GetCustomerId(), created.GetCustomerId())
	}
}
