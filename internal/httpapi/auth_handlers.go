package httpapi

import (
	"net/http"

	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/auth"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/request"
)

func (a *API) login(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeJSON(w, r, a.maxBytes, &input); err != nil {
		writeError(w, r, err)
		return
	}
	result, err := a.auth.Login(r.Context(), input.Username, input.Password, request.ID(r.Context()))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (a *API) logout(w http.ResponseWriter, r *http.Request) {
	principal, ok := a.principal(r)
	if !ok {
		writeError(w, r, commonUnauthorized())
		return
	}
	if err := a.auth.Logout(r.Context(), principal, request.ID(r.Context())); err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) me(w http.ResponseWriter, r *http.Request) {
	principal, ok := a.principal(r)
	if !ok {
		writeError(w, r, commonUnauthorized())
		return
	}
	writeJSON(w, http.StatusOK, principal)
}

func (a *API) createUser(w http.ResponseWriter, r *http.Request) {
	principal, ok := a.principal(r)
	if !ok {
		writeError(w, r, commonUnauthorized())
		return
	}
	var input struct {
		Username string    `json:"username"`
		Password string    `json:"password"`
		Role     auth.Role `json:"role"`
	}
	if err := decodeJSON(w, r, a.maxBytes, &input); err != nil {
		writeError(w, r, err)
		return
	}
	user, err := a.auth.CreateUser(r.Context(), principal, input.Username, input.Password, input.Role)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, user)
}
