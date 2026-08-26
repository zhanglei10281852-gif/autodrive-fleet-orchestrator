package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/auth"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/common"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/middleware"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/service"
)

type Readiness interface {
	Ping(context.Context) error
}

type API struct {
	auth       *service.AuthService
	fleet      *service.FleetService
	dispatch   *service.DispatchService
	operations *service.OperationsService
	resources  *service.ResourceService
	readiness  Readiness
	logger     *slog.Logger
	maxBytes   int64
	mux        *http.ServeMux
}

func New(authService *service.AuthService, fleetService *service.FleetService,
	dispatchService *service.DispatchService, operationsService *service.OperationsService,
	resourceService *service.ResourceService, readiness Readiness, logger *slog.Logger, maxBytes int64) *API {
	api := &API{auth: authService, fleet: fleetService, dispatch: dispatchService,
		operations: operationsService, resources: resourceService, readiness: readiness,
		logger: logger, maxBytes: maxBytes, mux: http.NewServeMux()}
	api.routes()
	return api
}

func (a *API) Handler() http.Handler { return a.mux }

func (a *API) routes() {
	a.mux.HandleFunc("GET /healthz", a.health)
	a.mux.HandleFunc("GET /readyz", a.ready)
	a.mux.HandleFunc("POST /v1/auth/login", a.login)
	a.mux.Handle("POST /v1/auth/logout", a.protected(http.HandlerFunc(a.logout)))
	a.mux.Handle("GET /v1/auth/me", a.protected(http.HandlerFunc(a.me)))
	a.mux.Handle("POST /v1/users", a.protected(http.HandlerFunc(a.createUser)))
	a.mux.Handle("POST /v1/regions", a.protected(http.HandlerFunc(a.createRegion)))
	a.mux.Handle("POST /v1/regions/{id}/transition", a.protected(http.HandlerFunc(a.transitionRegion)))
	a.mux.Handle("POST /v1/vehicles", a.protected(http.HandlerFunc(a.registerVehicle)))
	a.mux.Handle("GET /v1/vehicles", a.protected(http.HandlerFunc(a.listVehicles)))
	a.mux.Handle("POST /v1/vehicles/{id}/transition", a.protected(http.HandlerFunc(a.transitionVehicle)))
	a.mux.Handle("POST /v1/vehicles/{id}/safety-inspection", a.protected(http.HandlerFunc(a.safetyInspection)))
	a.mux.Handle("POST /v1/missions", a.protected(http.HandlerFunc(a.createMission)))
	a.mux.Handle("GET /v1/missions", a.protected(http.HandlerFunc(a.listMissions)))
	a.mux.Handle("POST /v1/missions/{id}/dispatch", a.protected(http.HandlerFunc(a.dispatchMission)))
	a.mux.Handle("POST /v1/missions/{id}/cancel", a.protected(http.HandlerFunc(a.cancelMission)))
	a.mux.Handle("POST /v1/trips/{id}/start", a.protected(http.HandlerFunc(a.startTrip)))
	a.mux.Handle("POST /v1/trips/{id}/complete", a.protected(http.HandlerFunc(a.completeTrip)))
	a.mux.HandleFunc("POST /v1/telemetry/batches", a.telemetryBatch)
	a.mux.Handle("GET /v1/incidents", a.protected(http.HandlerFunc(a.listIncidents)))
	a.mux.Handle("POST /v1/incidents/{id}/claim", a.protected(http.HandlerFunc(a.claimIncident)))
	a.mux.Handle("POST /v1/incidents/{id}/mitigation", a.protected(http.HandlerFunc(a.startMitigation)))
	a.mux.Handle("POST /v1/incidents/{id}/resolve", a.protected(http.HandlerFunc(a.resolveIncident)))
	a.mux.Handle("POST /v1/charging/stations", a.protected(http.HandlerFunc(a.createStation)))
	a.mux.Handle("POST /v1/charging/connectors", a.protected(http.HandlerFunc(a.createConnector)))
	a.mux.Handle("POST /v1/charging/sessions", a.protected(http.HandlerFunc(a.reserveCharging)))
	a.mux.Handle("POST /v1/charging/sessions/{id}/start", a.protected(http.HandlerFunc(a.startCharging)))
	a.mux.Handle("POST /v1/charging/sessions/{id}/complete", a.protected(http.HandlerFunc(a.completeCharging)))
	a.mux.Handle("POST /v1/maintenance/orders", a.protected(http.HandlerFunc(a.openMaintenance)))
	a.mux.Handle("POST /v1/maintenance/orders/{id}/start", a.protected(http.HandlerFunc(a.startMaintenance)))
	a.mux.Handle("POST /v1/maintenance/orders/{id}/checks", a.protected(http.HandlerFunc(a.recordMaintenanceCheck)))
	a.mux.Handle("POST /v1/maintenance/orders/{id}/complete", a.protected(http.HandlerFunc(a.completeMaintenance)))
}

func (a *API) protected(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := strings.TrimSpace(r.Header.Get("Authorization"))
		if !strings.HasPrefix(header, "Bearer ") {
			writeError(w, r, commonUnauthorized())
			return
		}
		principal, err := a.auth.Authenticate(r.Context(), strings.TrimSpace(strings.TrimPrefix(header, "Bearer ")))
		if err != nil {
			var remembered bool
			principal, remembered = middleware.RecentPrincipalFor(r)
			if !remembered {
				writeError(w, r, err)
				return
			}
		}
		next.ServeHTTP(w, r.WithContext(middleware.WithPrincipal(r.Context(), principal)))
	})
}

func (a *API) principal(r *http.Request) (auth.Principal, bool) {
	return middleware.Principal(r.Context())
}

func (a *API) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "alive"})
}

func (a *API) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := a.readiness.Ping(ctx); err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func pagination(r *http.Request) (int, int) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	return limit, offset
}

func commonUnauthorized() error { return common.ErrUnauthorized }
