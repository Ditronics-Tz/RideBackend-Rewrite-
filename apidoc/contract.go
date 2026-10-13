package apidoc

import (
	"encoding/json"
	"reflect"
	"regexp"
	"strings"

	"ride-backend/models"
)

var operationName = regexp.MustCompile(`[^a-zA-Z0-9]+`)

// Generate emits deterministic JSON, which is valid YAML, with detailed current
// B3/B4 contracts and a clearly identified inventory for other existing modules.
func Generate() ([]byte, error) {
	inventory, err := Inventory()
	if err != nil {
		return nil, err
	}
	component := components()
	paths := object{}
	for _, route := range inventory {
		path, ok := paths[route.OpenAPIPath].(object)
		if !ok {
			path = object{}
			paths[route.OpenAPIPath] = path
		}
		operation := object{"operationId": strings.ToLower(strings.Trim(operationName.ReplaceAllString(route.Method+"_"+route.OpenAPIPath, "_"), "_")), "summary": route.Method + " " + route.OpenAPIPath, "description": "Existing registered handler. This early contract records the route and access realm; response details for this existing module are not yet expanded.", "x-contract-detail": "route-inventory", "x-fiber-path": route.Path, "x-go-handler": route.Handler}
		responses := object{"2XX": object{"description": "Existing handler response; see endpoint implementation for the current payload."}, "default": object{"$ref": "#/components/responses/Error"}}
		if mayRateLimit(route) {
			responses["429"] = object{"$ref": "#/components/responses/RateLimited"}
		}
		operation["responses"] = responses
		security, roles := realm(route)
		operation["security"] = security
		if len(roles) > 0 {
			operation["x-allowed-roles"] = roles
		}
		parameters := []any{}
		for _, name := range route.Parameters {
			schema := scalar("string")
			if (name == "id" || strings.HasSuffix(name, "Id")) && !strings.Contains(route.Path, "/files/licences/") {
				schema["format"] = "uuid"
			}
			parameters = append(parameters, object{"name": name, "in": "path", "required": true, "schema": schema})
			if name == "path" {
				parameters[len(parameters)-1].(object)["description"] = "Fiber static wildcard; may contain multiple relative path segments."
			}
		}
		parameters = append(parameters, object{"name": "X-Request-ID", "in": "header", "required": false, "description": "Safe incoming identifier is echoed; an invalid or absent identifier is replaced.", "schema": object{"type": "string", "pattern": "^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$", "maxLength": 64}})
		operation["parameters"] = parameters
		describeCurrent(route, operation, component["schemas"].(object))
		path[strings.ToLower(route.Method)] = operation
	}
	document := object{"openapi": "3.0.3", "info": object{"title": "Rida Backend API", "version": "1.0.0", "description": "Current Fiber routes. Generated offline with go run ./cmd/generate-openapi. B3 storage, B4 runtime/list and B5 auth/session contracts are expanded; remaining modules have route inventory entries. Unimplemented admin rides/tickets handlers return 501. Authentication uses separate app and staff bearer realms. Error JSON includes a request_id and optional fields. Paged endpoints use limit default 50, maximum 200, offset default 0."}, "servers": []any{object{"url": "/"}}, "paths": paths, "components": component, "x-inventory-exclusions": []string{"Fiber Use middleware registrations (GetRoutes(true))", "Implicit framework HEAD mirrors", "Framework OPTIONS/CORS preflight registrations"}}
	encoded, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(encoded, '\n'), nil
}

func realm(route Route) ([]any, []string) {
	path := strings.TrimPrefix(route.Path, "/api/v1")
	if path == "/health" || path == "/ready" || strings.HasPrefix(path, "/uploads") || strings.HasPrefix(path, "/profile-images/") || strings.HasPrefix(path, "/share/") || path == "/auth/register" || path == "/auth/login" || strings.HasPrefix(path, "/auth/google") || path == "/staff/auth/login" || path == "/staff/auth/refresh" {
		return []any{}, nil
	}
	if strings.HasPrefix(path, "/webhooks/payments/") {
		return []any{object{"PaymentSignature": []string{}}}, nil
	}
	if strings.HasPrefix(path, "/files/") {
		return []any{object{"AppBearer": []string{}}, object{"StaffBearer": []string{}}}, []string{"owning driver or active staff"}
	}
	if strings.HasPrefix(path, "/admin/") {
		adminOnly := strings.HasPrefix(path, "/admin/staff") || path == "/admin/audit-logs" || strings.HasSuffix(path, "/role") || strings.HasSuffix(path, "/ban") || route.Method == "DELETE" && strings.Contains(path, "/notes/") || route.Method == "POST" && (strings.Contains(path, "/data-requests/") || strings.HasSuffix(path, "/deletion-request")) || strings.HasPrefix(path, "/admin/payouts") || strings.HasPrefix(path, "/admin/wallets") || strings.HasPrefix(path, "/admin/reconciliation") || path == "/admin/payment-settings" || path == "/admin/payments/summary" || path == "/admin/payments/export" || path == "/admin/sos/export" || route.Method != "GET" && (strings.HasPrefix(path, "/admin/promos") || path == "/admin/referral-settings" || strings.HasPrefix(path, "/admin/fraud/rules"))
		roles := []string{"admin", "support"}
		if adminOnly {
			roles = []string{"admin"}
		}
		return []any{object{"StaffBearer": []string{}}}, roles
	}
	if strings.HasPrefix(path, "/staff/") {
		return []any{object{"StaffBearer": []string{}}}, []string{"admin", "support"}
	}
	return []any{object{"AppBearer": []string{}}}, nil
}

func describeCurrent(route Route, operation, schemas object) {
	path := strings.TrimPrefix(route.Path, "/api/v1")
	responses := operation["responses"].(object)
	set := func(status, description string, schema object) {
		delete(responses, "2XX")
		responses[status] = jsonResponse(description, schema)
		operation["x-contract-detail"] = "expanded"
		operation["description"] = description
	}
	params := func(values ...any) { operation["parameters"] = append(operation["parameters"].([]any), values...) }
	pagination := func() {
		params(query("limit", object{"type": "integer", "default": 50, "minimum": 1, "maximum": 200}), query("offset", object{"type": "integer", "default": 0, "minimum": 0}))
	}
	if path == "/health" || path == "/ready" {
		name := "Health"
		description := "Cheap liveness probe; does not contact the database."
		if path == "/ready" {
			name = "Ready"
			description = "Readiness probe pings the database with a two second timeout."
			responses["503"] = jsonResponse("Database is unavailable.", ref("ReadyUnavailable"))
		}
		set("200", description, ref(name))
		delete(responses, "429")
		return
	}
	if path == "/driver/documents/presign" {
		set("201", "Driver requests a five minute private PUT and a pending_upload document. Send every returned upload header. JPEG, PNG, WebP and PDF are accepted; vehicle_photo requires an image. Licence and insurance require future expires_at. Confirm after direct upload; pending files cannot be read.", ref("PresignDocumentResponse"))
		operation["requestBody"] = object{"required": true, "content": object{"application/json": object{"schema": ref("PresignDocumentInput")}}}
		responses["501"] = jsonResponse("STORAGE_DRIVER=local does not provide direct presigned uploads; use multipart /driver/documents.", ref("Error"))
		return
	}
	if path == "/driver/documents/:id/confirm" {
		set("200", "Owning driver confirms the pending upload. HEAD metadata and downloaded bytes must match requested size and content type, including magic bytes. The server copies validated bytes to an immutable final object, then submits the row. Confirmation retries return the submitted document; an expired upload returns 409.", ref("DriverDocument"))
		responses["409"] = jsonResponse("Expired upload or invalid pending upload state.", ref("Error"))
		return
	}
	if path == "/driver/documents" {
		set("201", "Legacy multipart document upload remains available with STORAGE_DRIVER=local. R2 mode directs clients to the presign flow.", ref("DriverDocument"))
		operation["requestBody"] = multipart(properties(object{"type": scalar("string"), "file": object{"type": "string", "format": "binary"}, "expires_at": scalar("string")}, "type", "file"))
		responses["409"] = jsonResponse("Review claim prevents this upload.", ref("Error"))
		responses["410"] = jsonResponse("R2 mode requires the document presign and confirm flow.", ref("Error"))
		return
	}
	if path == "/files/documents/:id" {
		delete(responses, "2XX")
		operation["x-contract-detail"] = "expanded"
		operation["description"] = "Owning active driver or active staff only. Every staff read is audit logged before delivery. R2 defaults to a 302 redirect to a five minute private GET; ?presign=true returns signed JSON. Local defaults to authenticated binary delivery and rejects ?presign=true with 501. Pending uploads return 409."
		params(query("presign", object{"type": "boolean", "default": false}))
		responses["200"] = object{"description": "Signed JSON for object files with presign=true; otherwise a local binary file.", "content": object{"application/json": object{"schema": ref("PresignedDocumentRead")}, "application/octet-stream": object{"schema": object{"type": "string", "format": "binary"}}}, "headers": object{"Cache-Control": object{"schema": scalar("string"), "description": "private, no-store"}, "X-Request-ID": object{"schema": scalar("string")}}}
		responses["302"] = redirect("Short lived private object URL.")
		responses["501"] = jsonResponse("Local files do not support presigned reads.", ref("Error"))
		return
	}
	if path == "/profile-images/:id" {
		delete(responses, "2XX")
		responses["302"] = redirect("Public profile image custom domain. No JWT is required; cache max-age is 60 seconds.")
		operation["description"] = "Public redirect derived from the stored profile object key; legacy local photos use /uploads. The database stores the object key and profile_picture_url is derived."
		operation["x-contract-detail"] = "expanded"
		return
	}
	if strings.HasPrefix(path, "/uploads") || path == "/files/licences/:id" {
		delete(responses, "2XX")
		responses["200"] = object{"description": "Legacy file bytes. Licence reads require the owner or active staff; staff reads are audited.", "content": object{"application/octet-stream": object{"schema": object{"type": "string", "format": "binary"}}}}
		return
	}
	if path == "/driver/profile/me/images" || path == "/passenger/profile/me/image" {
		name := "PassengerProfile"
		if strings.HasPrefix(path, "/driver") {
			name = "DriverProfile"
		}
		set("200", "Multipart profile_picture, maximum 5 MiB. R2 accepts JPEG, PNG or WebP by magic bytes and returns a profile whose profile_picture_url is a derived API redirect. Local mode returns the legacy public upload URL.", ref(name))
		operation["requestBody"] = multipart(properties(object{"profile_picture": object{"type": "string", "format": "binary"}}, "profile_picture"))
		if name == "DriverProfile" {
			responses["410"] = jsonResponse("licence_image uploads moved to the document endpoint.", ref("Error"))
		}
		return
	}
	if path == "/driver/profile/me" || path == "/driver/profile/:userId" || path == "/passenger/profile/me" || path == "/passenger/profile/:userId" {
		name := "PassengerProfile"
		if strings.HasPrefix(path, "/driver") {
			name = "DriverProfile"
		}
		description := "Current profile response."
		if strings.Contains(path, ":userId") {
			description = "Any authenticated app user currently receives the full profile. Sensitive field exposure is a documented B6 gap; no public DTO is implemented yet."
			operation["x-known-security-gap"] = "full profile exposed to other app users"
		}
		set("200", description, ref(name))
		return
	}
	if path == "/admin/drivers/:id/verify" {
		delete(responses, "2XX")
		responses["410"] = jsonResponse("Boolean verification is retired; claim and review individual documents.", ref("Error"))
		operation["deprecated"] = true
		operation["description"] = "Retired boolean verification always returns 410 after authorization."
		return
	}
	if path == "/admin/rides" || path == "/admin/tickets" {
		set("501", "The persisted administration workflow is unavailable. This endpoint returns an error until B8 implements it; no list payload or pagination metadata is emitted.", ref("Error"))
		operation["x-implementation-status"] = "unavailable-until-B8"
		return
	}
	if path == "/admin/tickets/:id" {
		set("501", "The persisted ticket workflow is unavailable. The handler returns an error until B8 implements saved support work.", ref("Error"))
		operation["x-implementation-status"] = "unavailable-until-B8"
		return
	}
	if path == "/admin/stats" {
		set("200", "Driver and passenger counts use SQL COUNT. operational_counts_available=false identifies the legacy zero active_rides/open_tickets/open_complaints fields until B8 implements real operational counts.", ref("Stats"))
		return
	}
	if path == "/admin/users/:id/history" {
		pagination()
		response := paged("status_events", ref("UserStatusEvent"), false)
		fields := response["properties"].(object)
		fields["verification_reviews"] = array(ref("VerificationReview"))
		fields["totals"] = properties(object{"status_events": scalar("integer"), "verification_reviews": scalar("integer")}, "status_events", "verification_reviews")
		response["required"] = append(response["required"].([]string), "verification_reviews", "totals")
		set("200", "Both history streams apply the same limit and offset. totals contains the separate stream counts; total is the larger count.", response)
		return
	}
	if path == "/ratings/user/:userId" {
		pagination()
		response := paged("ratings", ref("Rating"), false)
		fields := response["properties"].(object)
		fields["user_id"] = scalar("string")
		fields["average_rating"] = scalar("number")
		fields["total_ratings"] = scalar("integer")
		response["required"] = append(response["required"].([]string), "user_id", "average_rating", "total_ratings")
		set("200", "Ratings for the specified user, with overall average and count plus the current page.", response)
		return
	}
	if route.Method == "GET" {
		if detail, ok := listContracts[path]; ok {
			pagination()
			response := paged(detail.key, detail.item, detail.count)
			if path == "/admin/complaints" {
				response["properties"].(object)["reports"] = array(ref("Report"))
				operation["deprecated"] = true
			}
			set("200", "Current paged list; existing collection key is preserved alongside total, limit and offset.", response)
			for _, filter := range detail.filters {
				parameter := query(filter, scalar("string"))
				if path == "/admin/data-requests" && filter == "type" {
					parameter["required"] = true
					parameter["schema"] = object{"type": "string", "enum": []string{"deletion", "export"}}
				}
				params(parameter)
			}
			return
		}
	}
	if path == "/driver/verification/me" || path == "/driver/verification/me/submit" || path == "/admin/drivers/:id/verification" {
		set("200", "Current driver verification detail; document URLs are authenticated API routes.", ref("VerificationDetail"))
		return
	}
	if path == "/auth/login" || path == "/auth/register" {
		input, status := reflect.TypeOf(models.LoginInput{}), "200"
		if path == "/auth/register" {
			input = reflect.TypeOf(models.RegisterInput{})
			status = "201"
		}
		set(status, "App authentication returns a 15-minute access token in both token and access_token, plus a 30-day refresh token. Login failures use a generic response; registration validates password strength and may be rate limited.", ref("AppSessionToken"))
		operation["requestBody"] = object{"required": true, "content": object{"application/json": object{"schema": schemaFor(input, schemas)}}}
		responses["429"] = jsonResponse("Rate limit exceeded.", ref("Error"))
		if path == "/auth/login" {
			responses["401"] = jsonResponse("Invalid credentials.", ref("Error"))
		}
		return
	}
	if path == "/auth/refresh" {
		set("200", "Rotates the hashed refresh token. Reuse of a rotated token revokes the full token family. Responses use Cache-Control: no-store.", ref("AppSessionToken"))
		operation["requestBody"] = object{"required": true, "content": object{"application/json": object{"schema": properties(object{"refresh_token": scalar("string")}, "refresh_token")}}}
		responses["401"] = jsonResponse("Invalid or reused refresh token.", ref("Error"))
		responses["429"] = jsonResponse("Rate limit exceeded.", ref("Error"))
		return
	}
	if path == "/auth/logout" || path == "/auth/logout-all" {
		set("200", "Revokes the current app session immediately.", properties(object{"message": scalar("string")}, "message"))
		if path == "/auth/logout-all" {
			operation["description"] = "Revokes every app session owned by the authenticated user immediately."
		}
		return
	}
	if path == "/auth/sessions" {
		pagination()
		set("200", "Lists the authenticated user's app sessions. Refresh tokens and token hashes are never returned.", ref("AppSessionPage"))
		return
	}
	if path == "/auth/sessions/:id" {
		set("204", "Revokes this session when it belongs to the authenticated user.", object{"type": "object"})
		responses["404"] = jsonResponse("Session not found.", ref("Error"))
		return
	}
	if strings.HasPrefix(path, "/admin/users/:id/sessions") {
		pagination()
		set("200", "Admin or support can inspect this user's app sessions and lockout counters.", properties(object{"sessions": array(ref("AppSession")), "total": scalar("integer"), "limit": scalar("integer"), "offset": scalar("integer"), "locked_until": object{"type": "string", "format": "date-time", "nullable": true}, "failed_login_count": scalar("integer")}, "sessions", "total", "limit", "offset", "failed_login_count"))
		return
	}
	if path == "/admin/users/:id/security-events" {
		pagination()
		params(query("type", scalar("string")))
		set("200", "Admin or support can inspect app login and session security events.", ref("AppSecurityEventPage"))
		return
	}
	if path == "/admin/users/:id/revoke-sessions" {
		set("200", "Admin or support revokes all app sessions, or the session_id in the request body.", properties(object{"message": scalar("string")}, "message"))
		operation["requestBody"] = object{"required": false, "content": object{"application/json": object{"schema": properties(object{"session_id": object{"type": "string", "format": "uuid"}})}}}
		return
	}
	if path == "/admin/users/:id/unlock" {
		set("200", "Admin only. Clears the app login lockout and records an audit event.", properties(object{"message": scalar("string")}, "message"))
		return
	}
	if path == "/auth/google" {
		delete(responses, "2XX")
		responses["307"] = redirect("Current OAuth login redirects to Google's consent screen.")
		responses["503"] = jsonResponse("Google OAuth or its redirect allowlist is not configured.", ref("Error"))
		operation["description"] = "Creates short-lived one-use server-side state bound to an HttpOnly browser cookie, then redirects to Google. The configured redirect URI must be allowlisted."
		operation["x-contract-detail"] = "expanded"
		return
	}
	if path == "/auth/google/callback" {
		delete(responses, "2XX")
		responses["200"] = jsonResponse("Exchanges a validated one-use state and browser cookie, requires a verified Google email, and binds identities by Google subject. Tokens are returned in the response body only.", ref("AppSessionToken"))
		responses["400"] = jsonResponse("State, browser binding or authorization code is invalid.", ref("Error"))
		responses["401"] = jsonResponse("Google identity could not be verified.", ref("Error"))
		operation["x-contract-detail"] = "expanded"
		operation["description"] = "Callback for a short-lived one-use state bound to an HttpOnly browser cookie. App tokens are returned in JSON and never in the redirect URL."
		return
	}
	if path == "/account/export-requests/:id/download" {
		delete(responses, "2XX")
		responses["200"] = object{"description": "Authenticated owner's ready, unexpired local export archive.", "content": object{"application/zip": object{"schema": object{"type": "string", "format": "binary"}}}}
	}
	if strings.Contains(path, "/export") && route.Method == "GET" && strings.HasPrefix(path, "/admin/") {
		delete(responses, "2XX")
		responses["200"] = object{"description": "Existing CSV export response.", "content": object{"text/csv": object{"schema": scalar("string")}}}
	}
	if path == "/admin/sos/stream" {
		delete(responses, "2XX")
		responses["200"] = object{"description": "Existing server sent event stream.", "content": object{"text/event-stream": object{"schema": scalar("string")}}}
	}
}

func mayRateLimit(route Route) bool {
	path := strings.TrimPrefix(route.Path, "/api/v1")
	return strings.HasPrefix(path, "/share/") || path == "/rides/:rideId/location" || route.Method == "POST" && (path == "/wallet/topups" || path == "/rides/:rideId/pay" || path == "/driver/payouts" || path == "/promos/validate" || path == "/account/deletion-request" || path == "/account/export-request" || path == "/auth/phone/request-otp" || path == "/staff/auth/login")
}

func query(name string, schema object) object {
	return object{"name": name, "in": "query", "required": false, "schema": schema}
}
func multipart(schema object) object {
	return object{"required": true, "content": object{"multipart/form-data": object{"schema": schema}}}
}
func redirect(description string) object {
	return object{"description": description, "headers": object{"Location": object{"schema": object{"type": "string", "format": "uri"}}, "X-Request-ID": object{"schema": scalar("string")}}}
}

type listContract struct {
	key     string
	item    object
	count   bool
	filters []string
}

var listContracts = map[string]listContract{
	"/admin/users":                     {"users", ref("UserResponse"), true, []string{"role", "status", "verification_status", "q"}},
	"/admin/audit-logs":                {"logs", ref("StaffAuditLog"), false, []string{"q", "action", "actor", "actor_id", "from", "to"}},
	"/admin/staff":                     {"staff", ref("StaffUserResponse"), true, []string{"q", "role", "status"}},
	"/admin/verifications":             {"verifications", ref("DriverProfile"), true, []string{"status", "q", "expiring_soon"}},
	"/driver/notifications":            {"notifications", ref("DriverNotification"), false, nil},
	"/admin/users/:id/notes":           {"notes", ref("SupportNote"), false, nil},
	"/admin/reports":                   {"reports", ref("Report"), false, []string{"status", "category", "assignee", "priority", "user", "before"}},
	"/admin/complaints":                {"complaints", ref("Report"), true, []string{"status", "category", "assignee", "priority", "user", "before"}},
	"/admin/flags":                     {"flags", ref("AccountFlag"), false, []string{"status"}},
	"/blocks":                          {"blocks", ref("UserBlock"), false, nil},
	"/reports":                         {"reports", properties(object{"id": scalar("string"), "status": scalar("string"), "created_at": scalar("string")}, "id", "status", "created_at"), false, nil},
	"/emergency-contacts":              {"contacts", ref("EmergencyContact"), false, nil},
	"/admin/incidents":                 {"incidents", ref("IncidentReport"), false, []string{"status"}},
	"/incidents":                       {"incidents", ref("IncidentReport"), false, nil},
	"/admin/payments":                  {"payments", ref("PaymentTransaction"), false, []string{"status", "method", "user", "ride", "provider_ref", "min_amount", "max_amount", "from", "to"}},
	"/payments":                        {"payments", ref("PaymentTransaction"), false, []string{"from", "to"}},
	"/driver/payouts":                  {"payouts", ref("DriverPayout"), false, []string{"status"}},
	"/admin/refunds":                   {"refunds", ref("Refund"), false, []string{"status"}},
	"/admin/promos":                    {"promos", ref("PromoCode"), false, []string{"status", "q", "audience"}},
	"/admin/promos/:id/audience":       {"audience", ref("PromoAudience"), false, nil},
	"/admin/promos/:id/redemptions":    {"redemptions", ref("PromoRedemption"), false, nil},
	"/admin/referrals":                 {"referrals", ref("Referral"), false, nil},
	"/admin/reconciliation/runs":       {"runs", ref("ReconciliationRun"), false, nil},
	"/admin/reconciliation/exceptions": {"exceptions", ref("ReconciliationException"), false, nil},
	"/admin/data-requests":             {"requests", object{}, false, []string{"type", "status"}},
	"/account/export-requests":         {"requests", ref("DataExportRequest"), false, nil},
}
