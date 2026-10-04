package worker

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testToken = "test-token-123"

func env(vars map[string]string) func(string) string {
	return func(name string) string { return vars[name] }
}

func do(t *testing.T, h http.Handler, method, path, auth, body string) (int, map[string]any) {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if auth != "" {
		r.Header.Set("Authorization", auth)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("response is not JSON: %q", w.Body.String())
	}
	return w.Code, out
}

func TestHandler(t *testing.T) {
	h := NewHandler(env(map[string]string{"PRICING_API_TOKEN": testToken}))
	bearer := "Bearer " + testToken
	valid := `{"product_id":"P002","category":"accessories","unit_price":4500,"quantity":3}`

	cases := []struct {
		name, method, path, auth, body string
		status                         int
		code, field                    string
	}{
		{"priced", "POST", "/price", bearer, valid, 200, "", ""},
		{"no token", "POST", "/price", "", valid, 401, "UNAUTHORIZED", ""},
		{"wrong token", "POST", "/price", "Bearer nope", valid, 401, "UNAUTHORIZED", ""},
		{"not a bearer token", "POST", "/price", testToken, valid, 401, "UNAUTHORIZED", ""},
		{"wrong path", "POST", "/", bearer, valid, 404, "NOT_FOUND", ""},
		{"wrong method", "GET", "/price", bearer, "", 405, "METHOD_NOT_ALLOWED", ""},
		{"empty body", "POST", "/price", bearer, "", 400, "INVALID_INPUT", "body"},
		{"not JSON", "POST", "/price", bearer, "hello", 400, "INVALID_INPUT", "body"},
		{"array body", "POST", "/price", bearer, "[1,2]", 400, "INVALID_INPUT", "body"},
		{"quantity as a string", "POST", "/price", bearer,
			`{"product_id":"P002","category":"accessories","unit_price":4500,"quantity":"3"}`, 400, "INVALID_INPUT", "quantity"},
		{"unknown field", "POST", "/price", bearer,
			`{"product_id":"P002","category":"accessories","unit_price":4500,"quantity":3,"discount":0.5}`, 400, "INVALID_INPUT", "discount"},
		{"zero quantity", "POST", "/price", bearer,
			`{"product_id":"P002","category":"accessories","unit_price":4500,"quantity":0}`, 400, "INVALID_INPUT", "quantity"},
		{"over the limit", "POST", "/price", bearer,
			`{"product_id":"P001","category":"laptops","unit_price":75000,"quantity":20}`, 422, "PRICE_LIMIT_EXCEEDED", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, out := do(t, h, tc.method, tc.path, tc.auth, tc.body)
			if status != tc.status {
				t.Fatalf("status = %d, want %d (%v)", status, tc.status, out)
			}
			if tc.code == "" {
				if out["total"] != 12825.0 {
					t.Errorf("total = %v, want 12825", out["total"])
				}
				return
			}
			if out["error"] != tc.code {
				t.Errorf("error = %v, want %s", out["error"], tc.code)
			}
			if tc.field != "" && out["field"] != tc.field {
				t.Errorf("field = %v, want %s", out["field"], tc.field)
			}
			if msg, _ := out["message"].(string); msg == "" {
				t.Error("message is empty")
			}
		})
	}
}

func TestHandlerFailsClosedWithoutASecret(t *testing.T) {
	h := NewHandler(env(nil))
	status, out := do(t, h, "POST", "/price", "Bearer anything",
		`{"product_id":"P002","category":"accessories","unit_price":4500,"quantity":1}`)
	if status != 500 || out["error"] != "MISCONFIGURED" {
		t.Fatalf("got %d %v, want 500 MISCONFIGURED", status, out)
	}
}
