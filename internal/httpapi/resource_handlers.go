package httpapi

import (
	"net/http"

	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/service"
)

func (a *API) createStation(w http.ResponseWriter, r *http.Request) {
	principal, ok := a.principal(r)
	if !ok {
		writeError(w, r, commonUnauthorized())
		return
	}
	var input struct {
		RegionID string `json:"region_id"`
		Code     string `json:"code"`
		Name     string `json:"name"`
	}
	if err := decodeJSON(w, r, a.maxBytes, &input); err != nil {
		writeError(w, r, err)
		return
	}
	value, err := a.resources.CreateStation(r.Context(), principal, input.RegionID, input.Code, input.Name)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, value)
}

func (a *API) createConnector(w http.ResponseWriter, r *http.Request) {
	principal, ok := a.principal(r)
	if !ok {
		writeError(w, r, commonUnauthorized())
		return
	}
	var input struct {
		StationID string `json:"station_id"`
		Code      string `json:"code"`
		PowerKW   int    `json:"power_kw"`
	}
	if err := decodeJSON(w, r, a.maxBytes, &input); err != nil {
		writeError(w, r, err)
		return
	}
	value, err := a.resources.CreateConnector(r.Context(), principal, input.StationID, input.Code, input.PowerKW)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, value)
}

func (a *API) reserveCharging(w http.ResponseWriter, r *http.Request) {
	principal, ok := a.principal(r)
	if !ok {
		writeError(w, r, commonUnauthorized())
		return
	}
	var input service.ReserveChargingInput
	if err := decodeJSON(w, r, a.maxBytes, &input); err != nil {
		writeError(w, r, err)
		return
	}
	value, err := a.resources.ReserveCharging(r.Context(), principal, input)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, value)
}

func (a *API) startCharging(w http.ResponseWriter, r *http.Request) {
	principal, ok := a.principal(r)
	if !ok {
		writeError(w, r, commonUnauthorized())
		return
	}
	var input struct {
		Version int64 `json:"version"`
	}
	if err := decodeJSON(w, r, a.maxBytes, &input); err != nil {
		writeError(w, r, err)
		return
	}
	value, err := a.resources.StartCharging(r.Context(), principal, r.PathValue("id"), input.Version)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, value)
}

func (a *API) completeCharging(w http.ResponseWriter, r *http.Request) {
	principal, ok := a.principal(r)
	if !ok {
		writeError(w, r, commonUnauthorized())
		return
	}
	var input struct {
		Version         int64 `json:"version"`
		FinalBattery    int   `json:"final_battery"`
		EnergyWattHours int64 `json:"energy_watt_hours"`
	}
	if err := decodeJSON(w, r, a.maxBytes, &input); err != nil {
		writeError(w, r, err)
		return
	}
	value, err := a.resources.CompleteCharging(r.Context(), principal, r.PathValue("id"), input.Version, input.FinalBattery, input.EnergyWattHours)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, value)
}

func (a *API) openMaintenance(w http.ResponseWriter, r *http.Request) {
	principal, ok := a.principal(r)
	if !ok {
		writeError(w, r, commonUnauthorized())
		return
	}
	var input service.OpenMaintenanceInput
	if err := decodeJSON(w, r, a.maxBytes, &input); err != nil {
		writeError(w, r, err)
		return
	}
	value, err := a.resources.OpenMaintenance(r.Context(), principal, input)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, value)
}

func (a *API) startMaintenance(w http.ResponseWriter, r *http.Request) {
	principal, ok := a.principal(r)
	if !ok {
		writeError(w, r, commonUnauthorized())
		return
	}
	var input struct {
		Technician string `json:"technician"`
		Version    int64  `json:"version"`
	}
	if err := decodeJSON(w, r, a.maxBytes, &input); err != nil {
		writeError(w, r, err)
		return
	}
	value, err := a.resources.StartMaintenance(r.Context(), principal, r.PathValue("id"), input.Technician, input.Version)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, value)
}

func (a *API) recordMaintenanceCheck(w http.ResponseWriter, r *http.Request) {
	principal, ok := a.principal(r)
	if !ok {
		writeError(w, r, commonUnauthorized())
		return
	}
	var input struct {
		Check   string `json:"check"`
		Version int64  `json:"version"`
	}
	if err := decodeJSON(w, r, a.maxBytes, &input); err != nil {
		writeError(w, r, err)
		return
	}
	value, err := a.resources.RecordMaintenanceCheck(r.Context(), principal, r.PathValue("id"), input.Check, input.Version)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, value)
}

func (a *API) completeMaintenance(w http.ResponseWriter, r *http.Request) {
	principal, ok := a.principal(r)
	if !ok {
		writeError(w, r, commonUnauthorized())
		return
	}
	var input struct {
		Resolution string `json:"resolution"`
		Version    int64  `json:"version"`
	}
	if err := decodeJSON(w, r, a.maxBytes, &input); err != nil {
		writeError(w, r, err)
		return
	}
	value, err := a.resources.CompleteMaintenance(r.Context(), principal, r.PathValue("id"), input.Resolution, input.Version)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, value)
}
