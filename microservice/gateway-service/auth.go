package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	pb "ecommerce-microservices/proto"
)

type credentials struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type registration struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Address  string `json:"address"`
	Password string `json:"password"`
}

// ---------- handlers ----------

// handleRegister creates an account and signs the new customer
// in immediately, so registering and then having to log in is
// not two separate steps for the shopper.
func (g *Gateway) handleRegister(w http.ResponseWriter, r *http.Request) {
	var body registration
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, errorView{
			Code:  "INVALID_ARGUMENT",
			Error: "The request body could not be read as JSON.",
		})
		return
	}

	body.Name = strings.TrimSpace(body.Name)
	body.Email = strings.TrimSpace(body.Email)

	if body.Name == "" || body.Email == "" || body.Password == "" {
		writeError(w, http.StatusBadRequest, errorView{
			Code:  "INVALID_ARGUMENT",
			Error: "Name, email and password are all required.",
		})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), g.requestLimit)
	defer cancel()

	customer, err := g.customers.CreateCustomer(ctx, &pb.CreateCustomerRequest{
		Name:     body.Name,
		Email:    body.Email,
		Address:  strings.TrimSpace(body.Address),
		Password: body.Password,
	})
	if err != nil {
		writeGRPCError(w, "Customer Service", err)
		return
	}

	g.startSession(w, customer, http.StatusCreated)
}

// handleLogin verifies credentials with the Customer Service
// and, on success, issues a session cookie.
func (g *Gateway) handleLogin(w http.ResponseWriter, r *http.Request) {
	var body credentials
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, errorView{
			Code:  "INVALID_ARGUMENT",
			Error: "The request body could not be read as JSON.",
		})
		return
	}

	if strings.TrimSpace(body.Email) == "" || body.Password == "" {
		writeError(w, http.StatusBadRequest, errorView{
			Code:  "INVALID_ARGUMENT",
			Error: "Please enter both your email and your password.",
		})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), g.requestLimit)
	defer cancel()

	customer, err := g.customers.Authenticate(ctx, &pb.AuthenticateRequest{
		Email:    strings.TrimSpace(body.Email),
		Password: body.Password,
	})
	if err != nil {
		writeGRPCError(w, "Customer Service", err)
		return
	}

	g.startSession(w, customer, http.StatusOK)
}

// handleLogout ends the current session. It succeeds even when
// there is no session, so signing out is always safe to call.
func (g *Gateway) handleLogout(w http.ResponseWriter, r *http.Request) {
	g.sessions.Destroy(tokenFrom(r))
	clearSessionCookie(w)
	writeJSON(w, http.StatusOK, map[string]bool{"signed_out": true})
}

// handleMe returns the signed-in customer's own record, read
// back from the Customer Service so it is never stale.
func (g *Gateway) handleMe(w http.ResponseWriter, r *http.Request) {
	customerID, ok := g.sessions.Lookup(tokenFrom(r))
	if !ok {
		writeUnauthenticated(w)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), g.requestLimit)
	defer cancel()

	customer, err := g.customers.GetCustomer(ctx, &pb.GetCustomerRequest{CustomerId: customerID})
	if err != nil {
		// The session names a customer the service no longer
		// knows, so the session itself is no longer meaningful.
		g.sessions.Destroy(tokenFrom(r))
		clearSessionCookie(w)
		writeGRPCError(w, "Customer Service", err)
		return
	}

	writeJSON(w, http.StatusOK, customerView{
		CustomerID: customer.GetCustomerId(),
		Name:       customer.GetName(),
		Email:      customer.GetEmail(),
		Address:    customer.GetAddress(),
	})
}

// ---------- helpers ----------

// startSession issues a session for a customer and replies
// with their record.
func (g *Gateway) startSession(w http.ResponseWriter, customer *pb.Customer, httpStatus int) {
	token, err := g.sessions.Create(customer.GetCustomerId())
	if err != nil {
		writeError(w, http.StatusInternalServerError, errorView{
			Code:  "INTERNAL",
			Error: "Could not start a session. Please try again.",
		})
		return
	}

	setSessionCookie(w, token)

	writeJSON(w, httpStatus, customerView{
		CustomerID: customer.GetCustomerId(),
		Name:       customer.GetName(),
		Email:      customer.GetEmail(),
		Address:    customer.GetAddress(),
	})
}

func writeUnauthenticated(w http.ResponseWriter) {
	writeError(w, http.StatusUnauthorized, errorView{
		Code:  "UNAUTHENTICATED",
		Error: "Please sign in to continue.",
	})
}

// requireSession wraps a handler so it only runs for a signed-in
// customer, passing that customer's ID through instead of
// letting the request name whichever customer it likes.
func (g *Gateway) requireSession(
	handler func(http.ResponseWriter, *http.Request, string),
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		customerID, ok := g.sessions.Lookup(tokenFrom(r))
		if !ok {
			writeUnauthenticated(w)
			return
		}
		handler(w, r, customerID)
	}
}
