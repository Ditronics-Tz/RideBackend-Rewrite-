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
	for _, value := range []any{models.UserResponse{}, models.DriverProfile{}, models.PassengerProfile{}, models.DriverDocument{}, models.VerificationDetail{}, models.StaffUserResponse{}, models.StaffAuditLog{}, models.Rating{}, models.DriverNotification{}, models.SupportNote{}, models.UserStatusEvent{}, models.DataExportRequest{}, models.Report{}, models.UserBlock{}, models.EmergencyContact{}, models.AccountFlag{}, models.FraudFlag{}, models.IncidentReport{}, models.PaymentTransaction{}, models.DriverPayout{}, models.Refund{}, models.PromoCode{}, models.PromoAudience{}, models.PromoRedemption{}, models.Referral{}, models.ReconciliationRun{}, models.ReconciliationException{}, storage.PresignedURL{}} {
		schemaFor(reflect.TypeOf(value), schemas)
	}
	schemas["Error"] = properties(object{"error": scalar("string"), "request_id": scalar("string"), "fields": object{"type": "object", "additionalProperties": scalar("string")}, "code": scalar("string")}, "error", "request_id")
	schemas["Error"].(object)["additionalProperties"] = true
	schemas["Health"] = properties(object{"status": object{"type": "string", "enum": []string{"ok"}}, "version": scalar("string")}, "status", "version")
	schemas["Ready"] = properties(object{"status": scalar("string"), "database": scalar("string"), "version": scalar("string"), "uptime_seconds": object{"type": "integer", "minimum": 0}}, "status", "database", "version", "uptime_seconds")
	schemas["ReadyUnavailable"] = object{"allOf": []any{ref("Ready"), ref("Error")}}
	schemas["PresignDocumentInput"] = properties(object{"type": object{"type": "string", "enum": []string{"driving_licence", "vehicle_card", "national_id", "vehicle_photo", "insurance"}}, "content_type": object{"type": "string", "enum": []string{"image/jpeg", "image/png", "image/webp", "application/pdf"}}, "size": object{"type": "integer", "format": "int64", "minimum": 1, "maximum": 8388608}, "expires_at": object{"type": "string", "description": "Future YYYY-MM-DD or RFC3339; required for driving_licence and insurance."}}, "type", "content_type", "size")
	schemas["PresignDocumentResponse"] = properties(object{"document": ref("DriverDocument"), "upload": ref("PresignedURL")}, "document", "upload")
	schemas["PresignedDocumentRead"] = properties(object{"url": object{"type": "string", "format": "uri"}, "method": object{"type": "string", "enum": []string{"GET"}}, "headers": object{"type": "object", "additionalProperties": scalar("string")}, "expires_at": object{"type": "string", "format": "date-time"}, "content_type": scalar("string"), "size": object{"type": "integer", "format": "int64"}}, "url", "method", "headers", "expires_at", "content_type", "size")
	schemas["Stats"] = properties(object{"total_drivers": scalar("integer"), "total_passengers": scalar("integer"), "operational_counts_available": object{"type": "boolean", "enum": []bool{false}}, "active_rides": object{"type": "integer", "enum": []int{0}}, "open_tickets": object{"type": "integer", "enum": []int{0}}, "open_complaints": object{"type": "integer", "enum": []int{0}}}, "total_drivers", "total_passengers", "operational_counts_available", "active_rides", "open_tickets", "open_complaints")
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
