package apidoc

import (
	"reflect"
	"ride-backend/models"
	"strings"
)

func describeTransport(route Route, op, schemas object) bool {
	path := strings.TrimPrefix(route.Path, "/api/v1")
	shape := func(v any) object { return schemaFor(reflect.TypeOf(v), schemas) }
	var payload, response object
	var description string
	switch path {
	case "/admin/transport/settings", "/transport/settings":
		response = shape(models.TransportSettings{})
		description = "Read versioned whole-trip fees and shared booking availability."
		if route.Method == "PUT" {
			payload = response
			description = "Admin saves an explicit shared fee of TZS 500–1000 and hailing percentage of 0–100 using the current settings version."
		}
	case "/admin/shared-trips/:id":
		response = properties(object{"trip": shape(models.SharedView{}), "timeline": array(object{"type": "object"})}, "trip", "timeline")
		description = "Staff-only shared trip, receipt and bounded audit timeline with actor and cancellation reason."
	case "/admin/transport/assessments":
		response = properties(object{"assessments": array(shape(models.TransportAssessment{})), "total": scalar("integer")}, "assessments", "total")
		description = "Staff reads immutable per-mode fee assessments and pending wallet receivables. Paginate with limit/offset and filter status."
	case "/shared-trips/:id/driver-location":
		response = shape(models.DriverLocation{})
		description = "Only the trip driver and an active booked passenger can read a fresh driver fix with accuracy within 100m. No point returns 204; closed trips never expose location."
	case "/shared-trips/:id/rating":
		payload = properties(object{"score": scalar("integer"), "comment": scalar("string"), "seat_id": scalar("string")}, "score")
		response = shape(models.Rating{})
		description = "Completed shared-trip participants rate their counterpart. Drivers specify a completed seat_id; passengers rate their driver. Identical retries return the same natural-keyed rating; changed content conflicts."
	case "/shared-trips/:id/sos":
		payload = properties(object{"lat": scalar("number"), "lng": scalar("number"), "accuracy": scalar("number"), "silent": scalar("boolean")}, "lat", "lng")
		response = shape(models.SOSAlert{})
		description = "An active shared-trip participant raises a mode-aware SOS. Retries return the existing unresolved alert; staff see a shared_trip reference."
	case "/transport/watch":
		op["description"] = "Durable, scoped invalidation long poll. Send after cursor and trip_id, or lat/lng/radius_km. Response includes ordered event IDs and aggregate versions. Refetch authorized snapshots after any event or reset. Polling reads the transactional outbox on all instances; no in-memory broker is required."
		for _, name := range []string{"trip_id", "lat", "lng", "radius_km", "after"} {
			op["parameters"] = append(op["parameters"].([]any), object{"name": name, "in": "query", "schema": scalar("string")})
		}
		op["responses"] = object{"200": jsonResponse("Replayable invalidation hints", properties(object{"cursor": scalar("integer"), "events": array(shape(models.TransportEvent{})), "reset": scalar("boolean"), "server_time": scalar("integer")}, "cursor", "events", "reset", "server_time")), "default": object{"$ref": "#/components/responses/Error"}}

		return true
	case "/driver/rides/current":
		response = shape(models.RideResponse{})
		description = "Return the authenticated driver's active hailed ride."
	case "/shared-trips", "/shared-trips/:id", "/shared-trips/:id/:action", "/admin/shared-trips":
		// SharedView embeds the trip; its public JSON contains trip fields at the top level.
		extra := properties(object{"available": scalar("integer"), "held": scalar("integer"), "confirmed": scalar("integer"), "server_time": scalar("integer"), "my_seat": shape(models.SharedSeat{}), "manifest": array(shape(models.SharedSeat{})), "pin": scalar("string"), "version": scalar("integer"), "receipt": shape(models.TransportAssessment{}), "driver_name": scalar("string"), "passenger_names": object{"type": "object", "additionalProperties": scalar("string")}}, "available", "held", "confirmed", "server_time")
		response = object{"allOf": []any{shape(models.SharedTrip{}), extra}}
		description = "Read an authorized shared trip snapshot. A passenger sees only their own pickup code. Drivers see held and verified seats."
		if route.Method == "POST" {
			op["parameters"] = append(op["parameters"].([]any), object{"in": "header", "name": "Idempotency-Key", "required": true, "schema": object{"type": "string", "minLength": 8, "maxLength": 100}})
			if path == "/shared-trips" {
				payload = shape(models.SharedPublish{})
				description = "Approved driver publishes a fixed route, passenger capacity, offered seats, price per seat and wait."
			} else {
				payload = shape(models.SharedAction{})
				description = "Reserve or release your seat as a passenger. The owning driver can verify a pickup PIN, start, cancel or complete the trip."
			}
		} else if !strings.Contains(path, ":id") {
			response = properties(object{"trips": array(response)}, "trips")
			description = "List shared trips using scope=mine, or scope=nearby with lat, lng and radius_km. Staff receive the operational list. Use limit and offset for pagination."
			for _, name := range []string{"scope", "lat", "lng", "radius_km", "limit", "offset"} {
				op["parameters"] = append(op["parameters"].([]any), object{"in": "query", "name": name, "schema": scalar("string")})
			}
		}
	default:
		return false
	}
	op["x-contract-detail"] = "expanded"
	op["description"] = description
	responses := object{"200": jsonResponse(description, response), "default": object{"$ref": "#/components/responses/Error"}}
	if path == "/driver/rides/current" || path == "/shared-trips/:id/driver-location" {
		responses["204"] = object{"description": "No active hailed ride."}
	}
	op["responses"] = responses
	if payload != nil {
		op["requestBody"] = object{"required": true, "content": object{"application/json": object{"schema": payload}}}
	}
	return true
}
