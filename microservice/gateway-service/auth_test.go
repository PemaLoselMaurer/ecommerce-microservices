package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ==========================================================
// SessionStore
// ==========================================================

func TestSessionRoundTrip(t *testing.T) {
	store := NewSessionStore()

	token, err := store.Create("C001")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	customerID, ok := store.Lookup(token)
	if !ok {
		t.Fatal("the session was not found immediately after being created")
	}
	if customerID != "C001" {
		t.Errorf("customer ID: got %q, want C001", customerID)
	}
}

func TestSessionTokensAreLongRandomAndUnique(t *testing.T) {
	store := NewSessionStore()

	seen := make(map[string]bool)

	for i := 0; i < 200; i++ {
		token, err := store.Create("C001")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if seen[token] {
			t.Fatalf("token %q was issued twice", token)
		}
		seen[token] = true

		// 32 random bytes in base64url is 43 characters. A
		// guessable token would be as good as no session at all.
		if len(token) < 40 {
			t.Errorf("token %q is only %d characters, want at least 40", token, len(token))
		}
		if strings.Contains(token, "C001") {
			t.Errorf("token %q contains the customer ID it stands for", token)
		}
	}
}

func TestLookupRejectsUnknownAndEmptyTokens(t *testing.T) {
	store := NewSessionStore()
	_, _ = store.Create("C001")

	for _, token := range []string{"", "not-a-real-token", "C001"} {
		t.Run("token="+token, func(t *testing.T) {
			if _, ok := store.Lookup(token); ok {
				t.Errorf("token %q was accepted", token)
			}
		})
	}
}

func TestDestroyEndsTheSession(t *testing.T) {
	store := NewSessionStore()
	token, _ := store.Create("C001")

	store.Destroy(token)

	if _, ok := store.Lookup(token); ok {
		t.Error("the session survived being destroyed")
	}

	// Destroying twice, or destroying something that was never
	// a session, must be harmless.
	store.Destroy(token)
	store.Destroy("never-existed")
}

func TestExpiredSessionsAreRejectedAndDropped(t *testing.T) {
	store := NewSessionStore()

	token, _ := store.Create("C001")

	// Age the session past its lifetime by rewriting its
	// expiry, rather than waiting twelve hours.
	store.mu.Lock()
	store.sessions[token] = session{customerID: "C001", expiresAt: time.Now().Add(-time.Minute)}
	store.mu.Unlock()

	if _, ok := store.Lookup(token); ok {
		t.Fatal("an expired session was accepted")
	}

	store.mu.Lock()
	_, stillHeld := store.sessions[token]
	store.mu.Unlock()

	if stillHeld {
		t.Error("the expired session was not dropped from the store")
	}
}

func TestSessionsAreIsolatedBetweenCustomers(t *testing.T) {
	store := NewSessionStore()

	alice, _ := store.Create("C001")
	bob, _ := store.Create("C002")

	if id, _ := store.Lookup(alice); id != "C001" {
		t.Errorf("alice's token resolved to %q, want C001", id)
	}
	if id, _ := store.Lookup(bob); id != "C002" {
		t.Errorf("bob's token resolved to %q, want C002", id)
	}

	store.Destroy(alice)

	if _, ok := store.Lookup(bob); !ok {
		t.Error("signing one customer out ended another customer's session")
	}
}

func TestSessionStoreIsSafeUnderConcurrentUse(t *testing.T) {
	store := NewSessionStore()

	var wait sync.WaitGroup
	for i := 0; i < 50; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()

			token, err := store.Create("C001")
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			if _, ok := store.Lookup(token); !ok {
				t.Error("a session went missing immediately after creation")
			}
			store.Destroy(token)
		}()
	}
	wait.Wait()
}

// ==========================================================
// Sign in
// ==========================================================

func TestLoginIssuesAnHttpOnlySessionCookie(t *testing.T) {
	kit := newGatewayKit()

	response := kit.do(http.MethodPost, "/api/auth/login",
		`{"email":"alice@example.com","password":"alice1234"}`)

	if response.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 (%s)", response.Code, response.Body.String())
	}

	var cookie *http.Cookie
	for _, candidate := range response.Result().Cookies() {
		if candidate.Name == sessionCookie {
			cookie = candidate
		}
	}
	if cookie == nil {
		t.Fatal("no session cookie was set")
	}

	// HttpOnly is what stops a script on the page — injected or
	// otherwise — from reading the session token.
	if !cookie.HttpOnly {
		t.Error("the session cookie is not HttpOnly")
	}
	if cookie.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite: got %v, want Lax", cookie.SameSite)
	}
	if cookie.Path != "/" {
		t.Errorf("path: got %q, want /", cookie.Path)
	}
	if cookie.Value == "C001" {
		t.Error("the cookie carries the customer ID directly, so it could be forged by editing it")
	}
}

func TestLoginReturnsTheCustomerWithoutACredential(t *testing.T) {
	kit := newGatewayKit()

	response := kit.do(http.MethodPost, "/api/auth/login",
		`{"email":"alice@example.com","password":"alice1234"}`)

	var customer customerView
	if err := json.Unmarshal(response.Body.Bytes(), &customer); err != nil {
		t.Fatalf("could not decode the response: %v", err)
	}

	if customer.CustomerID != "C001" {
		t.Errorf("customer ID: got %q, want C001", customer.CustomerID)
	}
	if customer.Name != "Alice Nguyen" {
		t.Errorf("name: got %q, want Alice Nguyen", customer.Name)
	}
	if strings.Contains(response.Body.String(), "alice1234") {
		t.Error("the response echoes the password back")
	}
	if strings.Contains(response.Body.String(), "password") {
		t.Error("the response carries a password field")
	}
}

func TestLoginRejectsWrongCredentials(t *testing.T) {
	kit := newGatewayKit()

	response := kit.do(http.MethodPost, "/api/auth/login",
		`{"email":"alice@example.com","password":"wrong"}`)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status: got %d, want 401", response.Code)
	}

	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == sessionCookie && cookie.Value != "" {
			t.Error("a session cookie was issued for a failed sign-in")
		}
	}
}

func TestLoginRejectsIncompleteRequests(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"not JSON", `oops`},
		{"no email", `{"password":"alice1234"}`},
		{"no password", `{"email":"alice@example.com"}`},
		{"both blank", `{"email":"","password":""}`},
		{"whitespace email", `{"email":"   ","password":"alice1234"}`},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			kit := newGatewayKit()

			response := kit.do(http.MethodPost, "/api/auth/login", testCase.body)

			if response.Code != http.StatusBadRequest {
				t.Errorf("status: got %d, want 400 (%s)", response.Code, response.Body.String())
			}
		})
	}
}

func TestLoginReports503WhenTheCustomerServiceIsDown(t *testing.T) {
	kit := newGatewayKit()
	kit.customer.authErr = status.Error(codes.Unavailable, "connection refused")

	response := kit.do(http.MethodPost, "/api/auth/login",
		`{"email":"alice@example.com","password":"alice1234"}`)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status: got %d, want 503", response.Code)
	}
}

// ==========================================================
// Register
// ==========================================================

func TestRegisterCreatesAnAccountAndSignsIn(t *testing.T) {
	kit := newGatewayKit()

	response := kit.do(http.MethodPost, "/api/auth/register",
		`{"name":"Karma Dorji","email":"karma@example.com","address":"Norzin Lam","password":"karma1234"}`)

	if response.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201 (%s)", response.Code, response.Body.String())
	}

	// Registering should not then require a separate sign-in.
	var signedIn bool
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == sessionCookie && cookie.Value != "" {
			signedIn = true
		}
	}
	if !signedIn {
		t.Error("registering did not start a session")
	}

	var customer customerView
	_ = json.Unmarshal(response.Body.Bytes(), &customer)
	if customer.CustomerID != "C003" {
		t.Errorf("customer ID: got %q, want C003", customer.CustomerID)
	}
}

func TestRegisterRejectsIncompleteRequests(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"not JSON", `oops`},
		{"no name", `{"email":"x@example.com","password":"password1"}`},
		{"no email", `{"name":"X","password":"password1"}`},
		{"no password", `{"name":"X","email":"x@example.com"}`},
		{"blank name", `{"name":"   ","email":"x@example.com","password":"password1"}`},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			kit := newGatewayKit()

			response := kit.do(http.MethodPost, "/api/auth/register", testCase.body)

			if response.Code != http.StatusBadRequest {
				t.Errorf("status: got %d, want 400 (%s)", response.Code, response.Body.String())
			}
		})
	}
}

func TestRegisterSurfacesADuplicateEmailAsAConflict(t *testing.T) {
	kit := newGatewayKit()
	kit.customer.createErr = status.Error(codes.AlreadyExists, "an account already exists for alice@example.com")

	response := kit.do(http.MethodPost, "/api/auth/register",
		`{"name":"Impostor","email":"alice@example.com","password":"password1"}`)

	if response.Code != http.StatusConflict {
		t.Fatalf("status: got %d, want 409", response.Code)
	}

	view := decodeError(t, response)
	if view.Code != "AlreadyExists" {
		t.Errorf("code: got %q, want AlreadyExists", view.Code)
	}
}

// ==========================================================
// Me and sign out
// ==========================================================

func TestMeRequiresASession(t *testing.T) {
	kit := newGatewayKit()

	response := kit.do(http.MethodGet, "/api/auth/me", "")

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status: got %d, want 401", response.Code)
	}
}

func TestMeReturnsTheSignedInCustomer(t *testing.T) {
	kit := newGatewayKit()
	cookie := kit.signIn(t)

	response := kit.do(http.MethodGet, "/api/auth/me", "", cookie)

	if response.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", response.Code)
	}

	var customer customerView
	_ = json.Unmarshal(response.Body.Bytes(), &customer)

	if customer.CustomerID != "C001" {
		t.Errorf("customer ID: got %q, want C001", customer.CustomerID)
	}
	if customer.Email != "alice@example.com" {
		t.Errorf("email: got %q, want alice@example.com", customer.Email)
	}
}

func TestMeRejectsAForgedCookie(t *testing.T) {
	kit := newGatewayKit()

	// Someone who sets the cookie by hand must not get in: the
	// token is looked up server-side, never trusted as data.
	forged := &http.Cookie{Name: sessionCookie, Value: "C001"}

	response := kit.do(http.MethodGet, "/api/auth/me", "", forged)

	if response.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d, want 401 for a forged cookie", response.Code)
	}
}

func TestMeEndsASessionThatNamesAMissingCustomer(t *testing.T) {
	kit := newGatewayKit()
	cookie := kit.signIn(t)

	// The customer disappears from the Customer Service after
	// the session was issued, so the session is meaningless.
	kit.customer.getErr = status.Error(codes.NotFound, "customer with ID C001 not found")

	response := kit.do(http.MethodGet, "/api/auth/me", "", cookie)

	if response.Code != http.StatusNotFound {
		t.Fatalf("status: got %d, want 404", response.Code)
	}

	if _, stillValid := kit.gateway.sessions.Lookup(cookie.Value); stillValid {
		t.Error("the session was left intact although the customer no longer exists")
	}
}

func TestLogoutEndsTheSession(t *testing.T) {
	kit := newGatewayKit()
	cookie := kit.signIn(t)

	response := kit.do(http.MethodPost, "/api/auth/logout", "", cookie)

	if response.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", response.Code)
	}

	// The cookie is expired in the browser...
	var cleared bool
	for _, candidate := range response.Result().Cookies() {
		if candidate.Name == sessionCookie && candidate.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Error("the session cookie was not expired on sign-out")
	}

	// ...and the token stops working even if it is replayed.
	after := kit.do(http.MethodGet, "/api/auth/me", "", cookie)
	if after.Code != http.StatusUnauthorized {
		t.Errorf("a replayed token after sign-out returned %d, want 401", after.Code)
	}
}

func TestLogoutIsSafeWithoutASession(t *testing.T) {
	kit := newGatewayKit()

	response := kit.do(http.MethodPost, "/api/auth/logout", "")

	if response.Code != http.StatusOK {
		t.Errorf("status: got %d, want 200 — signing out should always be safe to call", response.Code)
	}
}

func TestOneCustomerCannotUseAnothersSession(t *testing.T) {
	kit := newGatewayKit()

	aliceCookie := kit.signIn(t)

	// A second account exists with its own session.
	kit.customer.customers["C002"] = kit.customer.customers["C001"]
	bobToken, _ := kit.gateway.sessions.Create("C002")

	// Alice's cookie must resolve to Alice, and Bob's to Bob,
	// no matter what either request body claims.
	if id, _ := kit.gateway.sessions.Lookup(aliceCookie.Value); id != "C001" {
		t.Errorf("alice's session resolved to %q, want C001", id)
	}
	if id, _ := kit.gateway.sessions.Lookup(bobToken); id != "C002" {
		t.Errorf("bob's session resolved to %q, want C002", id)
	}
}
