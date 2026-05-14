package handler_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"rider-assignment-service/internal/domain"
	"rider-assignment-service/internal/handler"
	"rider-assignment-service/internal/repository"
)

// ---------------------------------------------------------------------------
// Mock services
// ---------------------------------------------------------------------------

type mockOrderSvc struct {
	order *domain.Order
	err   error
}

func (m *mockOrderSvc) CreateOrder(_ context.Context, _, _ float64) (*domain.Order, error) {
	return m.order, m.err
}
func (m *mockOrderSvc) GetOrder(_ context.Context, _ string) (*domain.Order, error) {
	return m.order, m.err
}
func (m *mockOrderSvc) AssignRider(_ context.Context, _ string) (*domain.Order, error) {
	return m.order, m.err
}
func (m *mockOrderSvc) UpdateOrderStatus(_ context.Context, _ string, _ domain.OrderStatus) (*domain.Order, error) {
	return m.order, m.err
}

// ---------------------------------------------------------------------------

type mockRiderSvc struct {
	rider *domain.Rider
	err   error
}

func (m *mockRiderSvc) RegisterRider(_ context.Context, _ string, _, _ float64) (*domain.Rider, error) {
	return m.rider, m.err
}
func (m *mockRiderSvc) UpdateRiderLocation(_ context.Context, _ string, _, _ float64) error {
	return m.err
}
func (m *mockRiderSvc) SetRiderAvailability(_ context.Context, _ string, _ bool) error {
	return m.err
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func orderMux(svc *mockOrderSvc) *http.ServeMux {
	mux := http.NewServeMux()
	handler.NewOrderHandler(svc).RegisterRoutes(mux)
	return mux
}

func riderMux(svc *mockRiderSvc) *http.ServeMux {
	mux := http.NewServeMux()
	handler.NewRiderHandler(svc).RegisterRoutes(mux)
	return mux
}

func do(mux *http.ServeMux, method, path, body string) *httptest.ResponseRecorder {
	var bodyReader *strings.Reader
	if body != "" {
		bodyReader = strings.NewReader(body)
	} else {
		bodyReader = strings.NewReader("")
	}
	req := httptest.NewRequest(method, path, bodyReader)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func decodeError(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var resp struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error response: %v — body: %s", err, rec.Body.String())
	}
	return resp.Code
}

func decodeOrder(t *testing.T, rec *httptest.ResponseRecorder) domain.Order {
	t.Helper()
	var o domain.Order
	if err := json.NewDecoder(rec.Body).Decode(&o); err != nil {
		t.Fatalf("decode order response: %v — body: %s", err, rec.Body.String())
	}
	return o
}

// ---------------------------------------------------------------------------
// Order handler tests
// ---------------------------------------------------------------------------

func TestCreateOrder(t *testing.T) {
	successOrder := &domain.Order{ID: "o1", Status: domain.StatusCreated, PickupLat: 1.23, PickupLng: 4.56}

	tests := []struct {
		name       string
		body       string
		svcOrder   *domain.Order
		svcErr     error
		wantStatus int
		wantCode   string
	}{
		{
			name:       "happy path — order created",
			body:       `{"pickup_lat":1.23,"pickup_lng":4.56}`,
			svcOrder:   successOrder,
			wantStatus: http.StatusCreated,
		},
		{
			name:       "malformed JSON body",
			body:       `not json`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "BAD_REQUEST",
		},
		{
			name:       "service error",
			body:       `{"pickup_lat":1.23,"pickup_lng":4.56}`,
			svcErr:     errors.New("db unavailable"),
			wantStatus: http.StatusInternalServerError,
			wantCode:   "INTERNAL_ERROR",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(orderMux(&mockOrderSvc{order: tt.svcOrder, err: tt.svcErr}),
				http.MethodPost, "/api/v1/orders", tt.body)

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d; want %d", rec.Code, tt.wantStatus)
			}
			if tt.wantCode != "" && decodeError(t, rec) != tt.wantCode {
				t.Errorf("code = %q; want %q", decodeError(t, rec), tt.wantCode)
			}
			if tt.svcOrder != nil {
				o := decodeOrder(t, rec)
				if o.ID != tt.svcOrder.ID {
					t.Errorf("response order ID = %q; want %q", o.ID, tt.svcOrder.ID)
				}
			}
		})
	}
}

func TestGetOrder(t *testing.T) {
	tests := []struct {
		name       string
		svcOrder   *domain.Order
		svcErr     error
		wantStatus int
		wantCode   string
	}{
		{
			name:       "found",
			svcOrder:   &domain.Order{ID: "o1", Status: domain.StatusAssigned},
			wantStatus: http.StatusOK,
		},
		{
			name:       "not found",
			svcErr:     fmt.Errorf("get order: %w", repository.ErrOrderNotFound),
			wantStatus: http.StatusNotFound,
			wantCode:   "ORDER_NOT_FOUND",
		},
		{
			name:       "internal error",
			svcErr:     errors.New("db error"),
			wantStatus: http.StatusInternalServerError,
			wantCode:   "INTERNAL_ERROR",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(orderMux(&mockOrderSvc{order: tt.svcOrder, err: tt.svcErr}),
				http.MethodGet, "/api/v1/orders/o1", "")

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d; want %d", rec.Code, tt.wantStatus)
			}
			if tt.wantCode != "" && decodeError(t, rec) != tt.wantCode {
				t.Errorf("code = %q; want %q", decodeError(t, rec), tt.wantCode)
			}
		})
	}
}

func TestAssignRider(t *testing.T) {
	assignedOrder := &domain.Order{ID: "o1", Status: domain.StatusAssigned}

	tests := []struct {
		name       string
		svcOrder   *domain.Order
		svcErr     error
		wantStatus int
		wantCode   string
	}{
		{
			name:       "happy path — rider assigned",
			svcOrder:   assignedOrder,
			wantStatus: http.StatusOK,
		},
		{
			name:       "no rider available within 5 km",
			svcErr:     repository.ErrNoRiderAvailable,
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   "NO_RIDER_AVAILABLE",
		},
		{
			name:       "order already assigned — invalid state transition",
			svcErr:     fmt.Errorf("assign rider: %w", domain.ErrInvalidTransition),
			wantStatus: http.StatusConflict,
			wantCode:   "INVALID_STATE_TRANSITION",
		},
		{
			name:       "order not found",
			svcErr:     fmt.Errorf("assign rider: %w", repository.ErrOrderNotFound),
			wantStatus: http.StatusNotFound,
			wantCode:   "ORDER_NOT_FOUND",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(orderMux(&mockOrderSvc{order: tt.svcOrder, err: tt.svcErr}),
				http.MethodPost, "/api/v1/orders/o1/assign", "")

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d; want %d", rec.Code, tt.wantStatus)
			}
			if tt.wantCode != "" && decodeError(t, rec) != tt.wantCode {
				t.Errorf("code = %q; want %q", decodeError(t, rec), tt.wantCode)
			}
			if tt.svcOrder != nil {
				o := decodeOrder(t, rec)
				if o.Status != domain.StatusAssigned {
					t.Errorf("response status = %q; want ASSIGNED", o.Status)
				}
			}
		})
	}
}

func TestUpdateOrderStatus(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		svcOrder   *domain.Order
		svcErr     error
		wantStatus int
		wantCode   string
	}{
		{
			name:       "happy path — status updated",
			body:       `{"status":"PICKED_UP"}`,
			svcOrder:   &domain.Order{ID: "o1", Status: domain.StatusPickedUp},
			wantStatus: http.StatusOK,
		},
		{
			name:       "invalid state transition",
			body:       `{"status":"DELIVERED"}`,
			svcErr:     fmt.Errorf("update order status: %w", domain.ErrInvalidTransition),
			wantStatus: http.StatusConflict,
			wantCode:   "INVALID_STATE_TRANSITION",
		},
		{
			name:       "missing status field",
			body:       `{}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "BAD_REQUEST",
		},
		{
			name:       "malformed JSON body",
			body:       `bad`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "BAD_REQUEST",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(orderMux(&mockOrderSvc{order: tt.svcOrder, err: tt.svcErr}),
				http.MethodPatch, "/api/v1/orders/o1/status", tt.body)

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d; want %d", rec.Code, tt.wantStatus)
			}
			if tt.wantCode != "" && decodeError(t, rec) != tt.wantCode {
				t.Errorf("code = %q; want %q", decodeError(t, rec), tt.wantCode)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Rider handler tests
// ---------------------------------------------------------------------------

func TestRegisterRider(t *testing.T) {
	successRider := &domain.Rider{ID: "r1", Name: "Alice", IsAvailable: true}

	tests := []struct {
		name       string
		body       string
		svcRider   *domain.Rider
		svcErr     error
		wantStatus int
		wantCode   string
	}{
		{
			name:       "happy path — rider registered",
			body:       `{"name":"Alice","lat":1.23,"lng":4.56}`,
			svcRider:   successRider,
			wantStatus: http.StatusCreated,
		},
		{
			name:       "missing name field",
			body:       `{"lat":1.23,"lng":4.56}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "BAD_REQUEST",
		},
		{
			name:       "malformed JSON body",
			body:       `not json`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "BAD_REQUEST",
		},
		{
			name:       "service error",
			body:       `{"name":"Alice","lat":1.23,"lng":4.56}`,
			svcErr:     errors.New("db unavailable"),
			wantStatus: http.StatusInternalServerError,
			wantCode:   "INTERNAL_ERROR",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(riderMux(&mockRiderSvc{rider: tt.svcRider, err: tt.svcErr}),
				http.MethodPost, "/api/v1/riders", tt.body)

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d; want %d", rec.Code, tt.wantStatus)
			}
			if tt.wantCode != "" && decodeError(t, rec) != tt.wantCode {
				t.Errorf("code = %q; want %q", decodeError(t, rec), tt.wantCode)
			}
			if tt.svcRider != nil {
				var r domain.Rider
				if err := json.NewDecoder(rec.Body).Decode(&r); err != nil {
					t.Fatalf("decode rider response: %v", err)
				}
				if r.Name != tt.svcRider.Name {
					t.Errorf("response rider name = %q; want %q", r.Name, tt.svcRider.Name)
				}
			}
		})
	}
}

func TestUpdateRiderLocation(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		svcErr     error
		wantStatus int
		wantCode   string
	}{
		{
			name:       "happy path — location updated",
			body:       `{"lat":1.23,"lng":4.56}`,
			wantStatus: http.StatusOK,
		},
		{
			name:       "rider not found",
			body:       `{"lat":1.23,"lng":4.56}`,
			svcErr:     fmt.Errorf("update rider location: %w", repository.ErrRiderNotFound),
			wantStatus: http.StatusNotFound,
			wantCode:   "RIDER_NOT_FOUND",
		},
		{
			name:       "malformed JSON body",
			body:       `bad`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "BAD_REQUEST",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(riderMux(&mockRiderSvc{err: tt.svcErr}),
				http.MethodPost, "/api/v1/riders/r1/location", tt.body)

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d; want %d", rec.Code, tt.wantStatus)
			}
			if tt.wantCode != "" && decodeError(t, rec) != tt.wantCode {
				t.Errorf("code = %q; want %q", decodeError(t, rec), tt.wantCode)
			}
		})
	}
}

func TestSetRiderAvailability(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		svcErr     error
		wantStatus int
		wantCode   string
	}{
		{
			name:       "set available — rider comes online",
			body:       `{"available":true}`,
			wantStatus: http.StatusOK,
		},
		{
			name:       "set unavailable — rider goes offline",
			body:       `{"available":false}`,
			wantStatus: http.StatusOK,
		},
		{
			name:       "rider not found",
			body:       `{"available":true}`,
			svcErr:     fmt.Errorf("set rider availability: %w", repository.ErrRiderNotFound),
			wantStatus: http.StatusNotFound,
			wantCode:   "RIDER_NOT_FOUND",
		},
		{
			name:       "malformed JSON body",
			body:       `bad`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "BAD_REQUEST",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(riderMux(&mockRiderSvc{err: tt.svcErr}),
				http.MethodPatch, "/api/v1/riders/r1/availability", tt.body)

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d; want %d", rec.Code, tt.wantStatus)
			}
			if tt.wantCode != "" && decodeError(t, rec) != tt.wantCode {
				t.Errorf("code = %q; want %q", decodeError(t, rec), tt.wantCode)
			}
		})
	}
}
