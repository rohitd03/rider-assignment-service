package handler

import (
	"net/http"

	"rider-assignment-service/internal/service"
)

type registerRiderRequest struct {
	Name string  `json:"name"`
	Lat  float64 `json:"lat"`
	Lng  float64 `json:"lng"`
}

type updateLocationRequest struct {
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}

type setAvailabilityRequest struct {
	Available bool `json:"available"`
}

// RiderHandler handles HTTP requests for rider management.
type RiderHandler struct {
	svc service.RiderService
}

func NewRiderHandler(svc service.RiderService) *RiderHandler {
	return &RiderHandler{svc: svc}
}

func (h *RiderHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/riders", h.registerRider)
	mux.HandleFunc("POST /api/v1/riders/{id}/location", h.updateLocation)
	mux.HandleFunc("PATCH /api/v1/riders/{id}/availability", h.setAvailability)
}

func (h *RiderHandler) registerRider(w http.ResponseWriter, r *http.Request) {
	var req registerRiderRequest
	if err := decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", "BAD_REQUEST")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required", "BAD_REQUEST")
		return
	}
	rider, err := h.svc.RegisterRider(r.Context(), req.Name, req.Lat, req.Lng)
	if err != nil {
		mapServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, rider)
}

func (h *RiderHandler) updateLocation(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req updateLocationRequest
	if err := decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", "BAD_REQUEST")
		return
	}
	if err := h.svc.UpdateRiderLocation(r.Context(), id, req.Lat, req.Lng); err != nil {
		mapServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, messageResponse{Message: "location updated"})
}

func (h *RiderHandler) setAvailability(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req setAvailabilityRequest
	if err := decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", "BAD_REQUEST")
		return
	}
	if err := h.svc.SetRiderAvailability(r.Context(), id, req.Available); err != nil {
		mapServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, messageResponse{Message: "availability updated"})
}
