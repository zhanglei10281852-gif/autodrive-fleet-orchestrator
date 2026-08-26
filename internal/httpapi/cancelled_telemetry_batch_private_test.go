package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/common"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/fleet"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/safety"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/telemetry"
)

func TestCancelledTelemetryBatchDoesNotPersistSideEffects(t *testing.T) {
	harness := newAPIHarness(t)
	now := harness.clock.Now()
	region := fleet.Region{
		ID: "reg-cancelled-batch", Code: "CANCEL", Name: "Cancellation Test Region",
		Timezone: "UTC", Status: fleet.RegionActive, MaxVehicles: 10, Version: 1,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := harness.store.CreateRegion(context.Background(), region); err != nil {
		t.Fatalf("create region: %v", err)
	}
	safetyUntil := now.Add(24 * time.Hour)
	vehicle := fleet.Vehicle{
		ID: "veh-cancelled-batch", RegionID: region.ID, VIN: "CANCELLED00000001",
		FleetNumber: "CANCEL-01", Status: fleet.VehicleAvailable, Capability: "urban",
		BatteryPercent: 88, Latitude: 31.23, Longitude: 121.47, SafetyValidUntil: &safetyUntil,
		Version: 1, CreatedAt: now, UpdatedAt: now,
	}
	if err := harness.store.CreateVehicle(context.Background(), vehicle); err != nil {
		t.Fatalf("create vehicle: %v", err)
	}

	cancelledSamples := []telemetry.Sample{
		{
			EventID: "telemetry-cancelled-info", VehicleID: vehicle.ID, ObservedAt: now.Add(-2 * time.Minute),
			Latitude: 31.24, Longitude: 121.48, SpeedKPH: 24, BatteryPercent: 87,
			OdometerMeters: 150020, Severity: telemetry.SeverityInfo,
		},
		{
			EventID: "telemetry-cancelled-critical", VehicleID: vehicle.ID, ObservedAt: now.Add(-time.Minute),
			Latitude: 31.25, Longitude: 121.49, SpeedKPH: 0, BatteryPercent: 86,
			OdometerMeters: 150025, FaultCode: "BRAKE-CONTROL", Severity: telemetry.SeverityCritical,
		},
	}
	body, err := json.Marshal(map[string]any{"samples": cancelledSamples})
	if err != nil {
		t.Fatalf("marshal cancelled batch: %v", err)
	}
	requestContext, cancel := context.WithCancel(context.Background())
	cancel()
	request := httptest.NewRequest(http.MethodPost, "/v1/telemetry/batches", bytes.NewReader(body)).WithContext(requestContext)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	harness.handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("cancelled batch status=%d body=%s", response.Code, response.Body.String())
	}
	var result telemetry.BatchResult
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode cancelled batch: %v", err)
	}
	if result.Accepted != 0 || result.Rejected != len(cancelledSamples) || len(result.Items) != len(cancelledSamples) {
		t.Fatalf("cancelled batch result=%+v", result)
	}
	for _, item := range result.Items {
		if item.Accepted || item.Code != "cancelled" {
			t.Fatalf("cancelled item persisted or used wrong code: %+v", item)
		}
	}
	for _, sample := range cancelledSamples {
		exists, err := harness.store.TelemetryEventExists(context.Background(), sample.EventID)
		if err != nil {
			t.Fatalf("check telemetry %s: %v", sample.EventID, err)
		}
		if exists {
			t.Fatalf("cancelled telemetry persisted: %s", sample.EventID)
		}
	}
	incidents, err := harness.store.ListIncidents(context.Background(), safety.Filter{
		VehicleID: vehicle.ID, Page: common.PageRequest{Limit: 20},
	})
	if err != nil {
		t.Fatalf("list incidents: %v", err)
	}
	if incidents.Total != 0 || len(incidents.Items) != 0 {
		t.Fatalf("cancelled fault created incidents: %+v", incidents)
	}

	activeSample := telemetry.Sample{
		EventID: "telemetry-active-after-cancel", VehicleID: vehicle.ID, ObservedAt: now,
		Latitude: 31.26, Longitude: 121.50, SpeedKPH: 18, BatteryPercent: 85,
		OdometerMeters: 150030, Severity: telemetry.SeverityInfo,
	}
	active := harness.request(t, http.MethodPost, "/v1/telemetry/batches", "", map[string]any{"samples": []telemetry.Sample{activeSample}})
	if active.Code != http.StatusAccepted {
		t.Fatalf("active batch status=%d body=%s", active.Code, active.Body.String())
	}
	exists, err := harness.store.TelemetryEventExists(context.Background(), activeSample.EventID)
	if err != nil {
		t.Fatalf("check active telemetry: %v", err)
	}
	if !exists {
		t.Fatal("active request did not persist valid telemetry")
	}
}
