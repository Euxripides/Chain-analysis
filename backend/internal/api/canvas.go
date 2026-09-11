package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/blockchain-platform/blockchain-platform/internal/canvas"
	"github.com/go-chi/chi/v5"
)

func (s *Server) handleListCanvases(w http.ResponseWriter, r *http.Request) {
	items, err := s.canvases.List(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"items": items})
}
func (s *Server) handleCreateCanvas(w http.ResponseWriter, r *http.Request) {
	var req canvas.CreateRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, 400, map[string]any{"error": err.Error()})
		return
	}
	item, err := s.canvases.Create(r.Context(), chi.URLParam(r, "id"), req)
	writeCanvas(w, item, err, http.StatusCreated)
}
func (s *Server) handleGetCanvas(w http.ResponseWriter, r *http.Request) {
	item, err := s.canvases.Get(r.Context(), chi.URLParam(r, "id"))
	writeCanvas(w, item, err, http.StatusOK)
}
func (s *Server) handleUpdateCanvas(w http.ResponseWriter, r *http.Request) {
	var req canvas.UpdateRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, 400, map[string]any{"error": err.Error()})
		return
	}
	rev, _ := strconv.ParseInt(r.Header.Get("If-Match"), 10, 64)
	item, err := s.canvases.Update(r.Context(), chi.URLParam(r, "id"), rev, req)
	writeCanvas(w, item, err, http.StatusOK)
}
func (s *Server) handleBranchCanvas(w http.ResponseWriter, r *http.Request) {
	var req canvas.CreateRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, 400, map[string]any{"error": err.Error()})
		return
	}
	item, err := s.canvases.Branch(r.Context(), chi.URLParam(r, "id"), req)
	writeCanvas(w, item, err, http.StatusCreated)
}
func (s *Server) handleCreateCanvasSnapshot(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, 400, map[string]any{"error": err.Error()})
		return
	}
	item, err := s.canvases.CreateSnapshot(r.Context(), chi.URLParam(r, "id"), req.Name)
	if err != nil {
		writeCanvasError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, item)
}
func (s *Server) handleListCanvasSnapshots(w http.ResponseWriter, r *http.Request) {
	items, err := s.canvases.ListSnapshots(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeCanvasError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"items": items})
}
func writeCanvas(w http.ResponseWriter, item canvas.Canvas, err error, status int) {
	if err != nil {
		writeCanvasError(w, err)
		return
	}
	writeJSON(w, status, item)
}
func writeCanvasError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	if errors.Is(err, canvas.ErrNotFound) {
		status = http.StatusNotFound
	}
	if errors.Is(err, canvas.ErrRevisionConflict) {
		status = http.StatusConflict
	}
	writeJSON(w, status, map[string]any{"error": err.Error()})
}
