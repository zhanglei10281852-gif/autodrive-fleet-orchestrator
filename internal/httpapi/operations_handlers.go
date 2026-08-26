package httpapi

import (
	"net/http"

	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/common"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/safety"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/telemetry"
)

func (a *API) telemetryBatch(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Samples []telemetry.Sample `json:"samples"`
	}
	if err := decodeJSON(w, r, a.maxBytes*4, &input); err != nil {
		writeError(w, r, err)
		return
	}
	result := a.operations.IngestTelemetry(r.Context(), input.Samples)
	status := http.StatusAccepted
	if result.Accepted == 0 && result.Rejected > 0 {
		status = http.StatusUnprocessableEntity
	}
	writeJSON(w, status, result)
}

func (a *API) listIncidents(w http.ResponseWriter, r *http.Request) {
	principal, ok := a.principal(r)
	if !ok {
		writeError(w, r, commonUnauthorized())
		return
	}
	limit, offset := pagination(r)
	page, err := a.operations.ListIncidents(r.Context(), principal, safety.Filter{
		VehicleID: r.URL.Query().Get("vehicle_id"), Status: safety.Status(r.URL.Query().Get("status")),
		Severity: safety.Severity(r.URL.Query().Get("severity")), OwnerID: r.URL.Query().Get("owner_id"),
		Page: common.PageRequest{Limit: limit, Offset: offset},
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (a *API) claimIncident(w http.ResponseWriter, r *http.Request) {
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
	value, err := a.operations.ClaimIncident(r.Context(), principal, r.PathValue("id"), input.Version)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, value)
}

func (a *API) startMitigation(w http.ResponseWriter, r *http.Request) {
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
	value, err := a.operations.StartMitigation(r.Context(), principal, r.PathValue("id"), input.Version)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, value)
}

func (a *API) resolveIncident(w http.ResponseWriter, r *http.Request) {
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
	value, err := a.operations.ResolveIncident(r.Context(), principal, r.PathValue("id"), input.Resolution, input.Version)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, value)
}
