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
	case "/transport/watch":
		op["description"] = "Wait up to two seconds for a nearby pickup or participant trip change, then refresh the snapshot. Use lat/lng or a participant trip_id."
		for _, name := range []string{"trip_id", "lat", "lng"} {
			op["parameters"] = append(op["parameters"].([]any), object{"name": name, "in": "query", "schema": scalar("string")})
		}
		op["responses"] = object{"204": object{"description": "Refresh the snapshot."}, "default": object{"$ref": "#/components/responses/Error"}}
		return true
	case "/driver/rides/current":
		response = shape(models.RideResponse{})
		description = "Return the authenticated driver's active hailed ride."
	case "/shared-trips", "/shared-trips/:id", "/shared-trips/:id/:action", "/admin/shared-trips":
		// SharedView embeds the trip; its public JSON contains trip fields at the top level.
		extra := properties(object{"available": scalar("integer"), "held": scalar("integer"), "confirmed": scalar("integer"), "server_time": scalar("integer"), "my_seat": shape(models.SharedSeat{}), "manifest": array(shape(models.SharedSeat{})), "pin": scalar("string")}, "available", "held", "confirmed", "server_time")
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
	if path == "/driver/rides/current" {
		responses["204"] = object{"description": "No active hailed ride."}
	}
	op["responses"] = responses
	if payload != nil {
		op["requestBody"] = object{"required": true, "content": object{"application/json": object{"schema": payload}}}
	}
	return true
}
