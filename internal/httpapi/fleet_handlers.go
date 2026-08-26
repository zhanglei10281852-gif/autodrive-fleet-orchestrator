package httpapi

import (
	"net/http"
	"strconv"
	"time"

	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/common"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/fleet"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/service"
)

func (a *API) createRegion(w http.ResponseWriter, r *http.Request) {
	principal, ok := a.principal(r)
	if !ok {
		writeError(w, r, commonUnauthorized())
		return
	}
	var input service.CreateRegionInput
	if err := decodeJSON(w, r, a.maxBytes, &input); err != nil {
		writeError(w, r, err)
		return
	}
	region, err := a.fleet.CreateRegion(r.Context(), principal, input)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, region)
}

func (a *API) transitionRegion(w http.ResponseWriter, r *http.Request) {
	principal, ok := a.principal(r)
	if !ok {
		writeError(w, r, commonUnauthorized())
		return
	}
	var input struct {
		Status  fleet.RegionStatus `json:"status"`
		Version int64              `json:"version"`
	}
	if err := decodeJSON(w, r, a.maxBytes, &input); err != nil {
		writeError(w, r, err)
		return
	}
	region, err := a.fleet.TransitionRegion(r.Context(), principal, r.PathValue("id"), input.Status, input.Version)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, region)
}

func (a *API) registerVehicle(w http.ResponseWriter, r *http.Request) {
	principal, ok := a.principal(r)
	if !ok {
		writeError(w, r, commonUnauthorized())
		return
	}
	var input service.RegisterVehicleInput
	if err := decodeJSON(w, r, a.maxBytes, &input); err != nil {
		writeError(w, r, err)
		return
	}
	vehicle, err := a.fleet.RegisterVehicle(r.Context(), principal, input)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, vehicle)
}

func (a *API) listVehicles(w http.ResponseWriter, r *http.Request) {
	principal, ok := a.principal(r)
	if !ok {
		writeError(w, r, commonUnauthorized())
		return
	}
	limit, offset := pagination(r)
	minimumBattery, _ := strconv.Atoi(r.URL.Query().Get("min_battery"))
	page, err := a.fleet.ListVehicles(r.Context(), principal, fleet.Filter{
		RegionID: r.URL.Query().Get("region_id"), Status: fleet.VehicleStatus(r.URL.Query().Get("status")),
		Capability: r.URL.Query().Get("capability"), MinBattery: minimumBattery,
		Search: r.URL.Query().Get("search"), Page: common.PageRequest{Limit: limit, Offset: offset},
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (a *API) transitionVehicle(w http.ResponseWriter, r *http.Request) {
	principal, ok := a.principal(r)
	if !ok {
		writeError(w, r, commonUnauthorized())
		return
	}
	var input struct {
		Status  fleet.VehicleStatus `json:"status"`
		Version int64               `json:"version"`
	}
	if err := decodeJSON(w, r, a.maxBytes, &input); err != nil {
		writeError(w, r, err)
		return
	}
	vehicle, err := a.fleet.TransitionVehicle(r.Context(), principal, r.PathValue("id"), input.Status, input.Version)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, vehicle)
}

func (a *API) safetyInspection(w http.ResponseWriter, r *http.Request) {
	principal, ok := a.principal(r)
	if !ok {
		writeError(w, r, commonUnauthorized())
		return
	}
	var input struct {
		ValidUntil time.Time `json:"valid_until"`
		Version    int64     `json:"version"`
	}
	if err := decodeJSON(w, r, a.maxBytes, &input); err != nil {
		writeError(w, r, err)
		return
	}
	if err := a.fleet.RecordSafetyInspection(r.Context(), principal, r.PathValue("id"), input.ValidUntil, input.Version); err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
