package apidoc

import (
	"reflect"
	"sort"
	"strings"
	"time"

	"ride-backend/models"
	"ride-backend/storage"
)

type object = map[string]any

func ref(name string) object    { return object{"$ref": "#/components/schemas/" + name} }
func scalar(kind string) object { return object{"type": kind} }
func array(items object) object { return object{"type": "array", "items": items} }
func properties(fields object, required ...string) object {
	schema := object{"type": "object", "properties": fields}
	if len(required) > 0 {
		sort.Strings(required)
		schema["required"] = required
	}
	return schema
}

func schemaFor(kind reflect.Type, schemas object) object {
	if kind.Kind() == reflect.Pointer {
		result := schemaFor(kind.Elem(), schemas)
		if _, hasRef := result["$ref"]; hasRef {
			return object{"allOf": []any{result}, "nullable": true}
		}
		result["nullable"] = true
		return result
	}
	if kind == reflect.TypeOf(time.Time{}) {
		return object{"type": "string", "format": "date-time"}
	}
	switch kind.Kind() {
	case reflect.String:
		return scalar("string")
	case reflect.Bool:
		return scalar("boolean")
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint64:
		return object{"type": "integer", "format": "int64"}
	case reflect.Float32, reflect.Float64:
		return object{"type": "number", "format": "double"}
	case reflect.Slice, reflect.Array:
		return array(schemaFor(kind.Elem(), schemas))
	case reflect.Map:
		return object{"type": "object", "additionalProperties": schemaFor(kind.Elem(), schemas)}
	case reflect.Struct:
		name := kind.Name()
		if _, exists := schemas[name]; exists {
			return ref(name)
		}
		schemas[name] = object{}
		fields := object{}
		required := []string{}
		for i := 0; i < kind.NumField(); i++ {
			field := kind.Field(i)
			tag := field.Tag.Get("json")
			if field.PkgPath != "" || tag == "-" {
				continue
			}
			parts := strings.Split(tag, ",")
			key := parts[0]
			if key == "" {
				key = field.Name
			}
			value := schemaFor(field.Type, schemas)
			if strings.Contains(field.Tag.Get("gorm"), "type:uuid") && field.Type.Kind() == reflect.String {
				value["format"] = "uuid"
			}
			fields[key] = value
			if !strings.Contains(tag, ",omitempty") {
				required = append(required, key)
			}
		}
		schemas[name] = properties(fields, required...)
		return ref(name)
	default:
		return object{}
	}
}

func components() object {
	schemas := object{}
	for _, value := range []any{models.UserResponse{}, models.DriverProfile{}, models.PassengerProfile{}, models.PublicDriverProfile{}, models.PublicPassengerProfile{}, models.DriverDocument{}, models.VerificationDetail{}, models.StaffUserResponse{}, models.StaffAuditLog{}, models.Rating{}, models.SupportTicket{}, models.SupportTicketNote{}, models.CreateSupportTicketInput{}, models.RateRideInput{}, models.ModerateRatingInput{}, models.DriverNotification{}, models.SupportNote{}, models.UserStatusEvent{}, models.DataExportRequest{}, models.Report{}, models.UserBlock{}, models.EmergencyContact{}, models.AccountFlag{}, models.FraudFlag{}, models.IncidentReport{}, models.PaymentTransaction{}, models.DriverPayout{}, models.Refund{}, models.PromoCode{}, models.PromoAudience{}, models.PromoRedemption{}, models.Referral{}, models.ReconciliationRun{}, models.ReconciliationException{}, models.RideResponse{}, models.RidePoint{}, models.RideParty{}, models.RideEventResponse{}, storage.PresignedURL{}} {
		schemaFor(reflect.TypeOf(value), schemas)
	}
	schemas["ModerateRatingInput"] = properties(object{"reason": object{"type": "string", "minLength": 1, "maxLength": 500}}, "reason")
	schemas["CreateRideInput"] = properties(object{"pickup": ref("RidePoint"), "dropoff": ref("RidePoint"), "vehicle_type": object{"type": "string", "maxLength": 50}}, "pickup", "dropoff")
	schemas["CancelRideInput"] = properties(object{"reason": object{"type": "string", "minLength": 1, "maxLength": 250}}, "reason")
	schemas["SetAvailabilityInput"] = properties(object{"online": scalar("boolean")}, "online")
	schemas["DriverLocationInput"] = properties(object{"lat": object{"type": "number", "minimum": -90, "maximum": 90}, "lng": object{"type": "number", "minimum": -180, "maximum": 180}, "accuracy": object{"type": "number", "minimum": 0, "maximum": 10000}}, "lat", "lng")
	schemas["RidePage"] = properties(object{"rides": array(ref("RideResponse")), "total": object{"type": "integer", "format": "int64"}, "limit": scalar("integer"), "offset": scalar("integer")}, "rides", "total", "limit", "offset")
	schemas["RideOffers"] = properties(object{"offers": array(ref("RideResponse"))}, "offers")
	schemas["SupportTicketPage"] = properties(object{"tickets": array(ref("SupportTicket")), "total": object{"type": "integer", "format": "int64"}, "limit": scalar("integer"), "offset": scalar("integer")}, "tickets", "total", "limit", "offset")
	schemas["SupportTicketDetail"] = properties(object{"ticket": ref("SupportTicket"), "notes": array(ref("SupportTicketNote")), "notes_total": object{"type": "integer", "format": "int64"}}, "ticket", "notes", "notes_total")
	schemas["WorkSupportTicketInput"] = properties(object{"action": object{"type": "string", "enum": []string{"assign", "note", "update"}}, "assigned_to": object{"type": "string", "format": "uuid"}, "status": object{"type": "string", "enum": []string{"open", "in_review", "resolved", "closed"}}, "priority": object{"type": "string", "enum": []string{"low", "normal", "high", "urgent"}}, "note": object{"type": "string", "maxLength": 4000}, "resolution_outcome": scalar("string"), "resolution_note": object{"type": "string", "maxLength": 2000}}, "action")
	schemas["AdminRidePage"] = schemas["RidePage"]
	schemas["AdminCancelRideInput"] = properties(object{"reason": object{"type": "string", "minLength": 1, "maxLength": 250}}, "reason")
	schemas["AdminReassignRideInput"] = properties(object{"driver_id": object{"type": "string", "format": "uuid"}, "reason": object{"type": "string", "minLength": 1, "maxLength": 250}}, "driver_id", "reason")
	schemas["Error"] = properties(object{"error": scalar("string"), "request_id": scalar("string"), "fields": object{"type": "object", "additionalProperties": scalar("string")}, "code": scalar("string")}, "error", "request_id")
	schemas["Error"].(object)["additionalProperties"] = true
	schemas["Health"] = properties(object{"status": object{"type": "string", "enum": []string{"ok"}}, "version": scalar("string")}, "status", "version")
	schemas["Ready"] = properties(object{"status": scalar("string"), "database": scalar("string"), "version": scalar("string"), "uptime_seconds": object{"type": "integer", "minimum": 0}}, "status", "database", "version", "uptime_seconds")
	schemas["ReadyUnavailable"] = object{"allOf": []any{ref("Ready"), ref("Error")}}
	schemas["AppSession"] = properties(object{"id": object{"type": "string", "format": "uuid"}, "device_id": scalar("string"), "platform": scalar("string"), "ip": scalar("string"), "created_at": object{"type": "string", "format": "date-time"}, "last_used_at": object{"type": "string", "format": "date-time"}, "expires_at": object{"type": "string", "format": "date-time"}, "revoked_at": object{"type": "string", "format": "date-time"}}, "id", "created_at", "last_used_at", "expires_at")
	schemas["AppSecurityEvent"] = properties(object{"type": scalar("string"), "ip": scalar("string"), "created_at": object{"type": "string", "format": "date-time"}}, "type", "created_at")
	schemas["AppSessionToken"] = properties(object{"message": scalar("string"), "token": scalar("string"), "access_token": scalar("string"), "refresh_token": scalar("string"), "expires_in": object{"type": "integer", "enum": []int{900}}, "refresh_expires_at": object{"type": "string", "format": "date-time"}, "restricted": scalar("boolean"), "user": ref("UserResponse")}, "message", "token", "access_token", "refresh_token", "expires_in", "refresh_expires_at", "restricted", "user")
	schemas["AppSessionPage"] = properties(object{"sessions": array(ref("AppSession")), "total": scalar("integer"), "limit": scalar("integer"), "offset": scalar("integer")}, "sessions", "total", "limit", "offset")
	schemas["AppSecurityEventPage"] = properties(object{"events": array(ref("AppSecurityEvent")), "total": scalar("integer"), "limit": scalar("integer"), "offset": scalar("integer")}, "events", "total", "limit", "offset")
	schemas["StaffFullProfile"] = properties(object{"user": ref("UserResponse"), "driver_profile": ref("DriverProfile"), "passenger_profile": ref("PassengerProfile")}, "user")
	schemas["PresignDocumentInput"] = properties(object{"type": object{"type": "string", "enum": []string{"driving_licence", "vehicle_card", "national_id", "vehicle_photo", "insurance"}}, "content_type": object{"type": "string", "enum": []string{"image/jpeg", "image/png", "image/webp", "application/pdf"}}, "size": object{"type": "integer", "format": "int64", "minimum": 1, "maximum": 8388608}, "expires_at": object{"type": "string", "description": "Future YYYY-MM-DD or RFC3339; required for driving_licence and insurance."}}, "type", "content_type", "size")
	schemas["PresignDocumentResponse"] = properties(object{"document": ref("DriverDocument"), "upload": ref("PresignedURL")}, "document", "upload")
	schemas["PresignedDocumentRead"] = properties(object{"url": object{"type": "string", "format": "uri"}, "method": object{"type": "string", "enum": []string{"GET"}}, "headers": object{"type": "object", "additionalProperties": scalar("string")}, "expires_at": object{"type": "string", "format": "date-time"}, "content_type": scalar("string"), "size": object{"type": "integer", "format": "int64"}}, "url", "method", "headers", "expires_at", "content_type", "size")
	schemas["Stats"] = properties(object{"total_drivers": scalar("integer"), "total_passengers": scalar("integer"), "operational_counts_available": object{"type": "boolean", "enum": []bool{true}}, "active_rides": object{"type": "integer", "minimum": 0}, "open_tickets": object{"type": "integer", "minimum": 0}, "open_complaints": object{"type": "integer", "minimum": 0}}, "total_drivers", "total_passengers", "operational_counts_available", "active_rides", "open_tickets", "open_complaints")
	return object{
		"schemas": schemas,
		"securitySchemes": object{
			"AppBearer":        object{"type": "http", "scheme": "bearer", "bearerFormat": "JWT", "description": "App realm access token for passenger or driver. Staff tokens are rejected. Current B4 app login returns token; app refresh/session endpoints are not yet implemented."},
			"StaffBearer":      object{"type": "http", "scheme": "bearer", "bearerFormat": "JWT", "description": "Staff realm access token; active staff and role checks apply, with password-change and configured admin 2FA gates."},
			"PaymentSignature": object{"type": "apiKey", "in": "header", "name": "X-Payment-Signature", "description": "Provider HMAC of timestamp + '.' + raw request body; X-Payment-Timestamp is also required."},
		},
		"responses": object{
			"Error":       object{"description": "Error envelope. request_id is the request tracing identifier; fields are present on field validation errors.", "headers": requestIDHeaders(), "content": object{"application/json": object{"schema": ref("Error")}}},
			"RateLimited": object{"description": "Rate limit exceeded. Retry-After is supplied by limiter responses when available; some legacy persistent export limits do not supply it yet.", "headers": object{"X-Request-ID": object{"schema": scalar("string")}, "Retry-After": object{"schema": scalar("string"), "description": "Delay in seconds when emitted by this limiter."}}, "content": object{"application/json": object{"schema": ref("Error")}}},
		},
	}
}

func requestIDHeaders() object { return object{"X-Request-ID": object{"schema": scalar("string")}} }
func jsonResponse(description string, schema object) object {
	return object{"description": description, "headers": requestIDHeaders(), "content": object{"application/json": object{"schema": schema}}}
}
func paged(key string, item object, count bool) object {
	fields := object{key: array(item), "total": object{"type": "integer", "format": "int64", "minimum": 0}, "limit": object{"type": "integer", "minimum": 1, "maximum": 200}, "offset": object{"type": "integer", "minimum": 0}}
	required := []string{key, "total", "limit", "offset"}
	if count {
		fields["count"] = scalar("integer")
		required = append(required, "count")
	}
	return properties(fields, required...)
}
