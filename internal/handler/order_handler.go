package handler

import (
	"encoding/json"
	"errors"
	"net/http"

	"rider-assignment-service/internal/domain"
	"rider-assignment-service/internal/repository"
	"rider-assignment-service/internal/service"
)

// errorResponse is the uniform error envelope returned on all non-2xx responses.
type errorResponse struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

type messageResponse struct {
	Message string `json:"message"`
}

type createOrderRequest struct {
	PickupLat float64 `json:"pickup_lat"`
	PickupLng float64 `json:"pickup_lng"`
}

type updateStatusRequest struct {
	Status domain.OrderStatus `json:"status"`
}

// OrderHandler handles HTTP requests for the order lifecycle.
type OrderHandler struct {
	svc service.AssignmentService
}

func NewOrderHandler(svc service.AssignmentService) *OrderHandler {
	return &OrderHandler{svc: svc}
}

func (h *OrderHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/orders", h.createOrder)
	mux.HandleFunc("GET /api/v1/orders/{id}", h.getOrder)
	mux.HandleFunc("POST /api/v1/orders/{id}/assign", h.assignRider)
	mux.HandleFunc("PATCH /api/v1/orders/{id}/status", h.updateOrderStatus)
}

func (h *OrderHandler) createOrder(w http.ResponseWriter, r *http.Request) {
	var req createOrderRequest
	if err := decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", "BAD_REQUEST")
		return
	}
	order, err := h.svc.CreateOrder(r.Context(), req.PickupLat, req.PickupLng)
	if err != nil {
		mapServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, order)
}

func (h *OrderHandler) getOrder(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	order, err := h.svc.GetOrder(r.Context(), id)
	if err != nil {
		mapServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, order)
}

func (h *OrderHandler) assignRider(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	order, err := h.svc.AssignRider(r.Context(), id)
	if err != nil {
		mapServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, order)
}

func (h *OrderHandler) updateOrderStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req updateStatusRequest
	if err := decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", "BAD_REQUEST")
		return
	}
	if req.Status == "" {
		writeError(w, http.StatusBadRequest, "status is required", "BAD_REQUEST")
		return
	}
	order, err := h.svc.UpdateOrderStatus(r.Context(), id, req.Status)
	if err != nil {
		mapServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, order)
}

// ---------------------------------------------------------------------------
// Shared JSON helpers — used by all handlers in this package.
// ---------------------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v) //nolint:errcheck
}

func writeError(w http.ResponseWriter, status int, message, code string) {
	writeJSON(w, status, errorResponse{Error: message, Code: code})
}

func decode(r *http.Request, v any) error {
	return json.NewDecoder(r.Body).Decode(v)
}

// mapServiceError translates domain/repository sentinel errors to HTTP status
// codes and structured error responses. Unknown errors become 500.
func mapServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, repository.ErrNoRiderAvailable):
		writeError(w, http.StatusUnprocessableEntity, "no rider available within range", "NO_RIDER_AVAILABLE")
	case errors.Is(err, domain.ErrInvalidTransition):
		writeError(w, http.StatusConflict, "invalid state transition", "INVALID_STATE_TRANSITION")
	case errors.Is(err, repository.ErrOrderNotFound):
		writeError(w, http.StatusNotFound, "order not found", "ORDER_NOT_FOUND")
	case errors.Is(err, repository.ErrRiderNotFound):
		writeError(w, http.StatusNotFound, "rider not found", "RIDER_NOT_FOUND")
	default:
		writeError(w, http.StatusInternalServerError, "internal server error", "INTERNAL_ERROR")
	}
}
