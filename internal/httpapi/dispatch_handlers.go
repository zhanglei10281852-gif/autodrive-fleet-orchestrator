package httpapi

import (
	"net/http"

	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/common"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/mission"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/service"
)

func (a *API) createMission(w http.ResponseWriter, r *http.Request) {
	principal, ok := a.principal(r)
	if !ok {
		writeError(w, r, commonUnauthorized())
		return
	}
	var input service.CreateMissionInput
	if err := decodeJSON(w, r, a.maxBytes, &input); err != nil {
		writeError(w, r, err)
		return
	}
	value, err := a.dispatch.CreateMission(r.Context(), principal, input)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, value)
}

func (a *API) listMissions(w http.ResponseWriter, r *http.Request) {
	principal, ok := a.principal(r)
	if !ok {
		writeError(w, r, commonUnauthorized())
		return
	}
	limit, offset := pagination(r)
	page, err := a.dispatch.ListMissions(r.Context(), principal, mission.Filter{
		RegionID: r.URL.Query().Get("region_id"), Status: mission.Status(r.URL.Query().Get("status")),
		Priority: mission.Priority(r.URL.Query().Get("priority")), Page: common.PageRequest{Limit: limit, Offset: offset},
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (a *API) dispatchMission(w http.ResponseWriter, r *http.Request) {
	principal, ok := a.principal(r)
	if !ok {
		writeError(w, r, commonUnauthorized())
		return
	}
	value, err := a.dispatch.Dispatch(r.Context(), principal, r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, value)
}

func (a *API) cancelMission(w http.ResponseWriter, r *http.Request) {
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
	if err := a.dispatch.CancelMission(r.Context(), principal, r.PathValue("id"), input.Version); err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) startTrip(w http.ResponseWriter, r *http.Request) {
	principal, ok := a.principal(r)
	if !ok {
		writeError(w, r, commonUnauthorized())
		return
	}
	value, err := a.dispatch.StartTrip(r.Context(), principal, r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, value)
}

func (a *API) completeTrip(w http.ResponseWriter, r *http.Request) {
	principal, ok := a.principal(r)
	if !ok {
		writeError(w, r, commonUnauthorized())
		return
	}
	var input struct {
		DistanceMeters int64 `json:"distance_meters"`
	}
	if err := decodeJSON(w, r, a.maxBytes, &input); err != nil {
		writeError(w, r, err)
		return
	}
	value, err := a.dispatch.CompleteTrip(r.Context(), principal, r.PathValue("id"), input.DistanceMeters)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, value)
}
