package apidoc

import (
	"fmt"
	"strings"
)

// GenerateRouteGuards records registered parameterized routes and the guards
// observed in route/controller/store code. It does not make implementation changes.
func GenerateRouteGuards() ([]byte, error) {
	inventory, err := Inventory()
	if err != nil {
		return nil, err
	}
	var result strings.Builder
	result.WriteString("# Route guards\n\nGenerated with `go run ./cmd/generate-openapi`. Each row records a concrete registered route containing a path parameter. Fiber middleware, HEAD mirrors and OPTIONS handlers are excluded. The OpenAPI artifact records all concrete routes.\n\n")
	result.WriteString("The two profile lookup routes return sensitive fields to other authenticated app users. B6 must introduce public DTOs and negative privacy tests. Legacy private file handlers use controller authentication, current app/staff session validation, and lexical local path checks; filesystem handling remains part of B6. Incident attachment reads have owner/staff checks but retain the existing local SendFile path handling; that existing module is outside this ship run.\n\n")
	result.WriteString("Admin ride, ticket, complaint, report and rating operations are persisted and staff-audited. Dashboard operational counts use live SQL counts. Staff scope is intentionally broader than object ownership, with route roles and controller action policy enforced.\n\n")
	result.WriteString("| Route | Access realm / role | Resource guard observed | Status | Handler |\n| --- | --- | --- | --- | --- |\n")
	for _, route := range inventory {
		if len(route.Parameters) == 0 {
			continue
		}
		access, guard, status := guardFor(route)
		fmt.Fprintf(&result, "| `%s %s` | %s | %s | %s | `%s` |\n", route.Method, route.Path, access, guard, status, route.Handler)
	}
	return []byte(result.String()), nil
}

func guardFor(route Route) (access, guard, status string) {
	path := strings.TrimPrefix(route.Path, "/api/v1")
	security, roles := realm(route)
	access = "public"
	if len(security) > 0 {
		access = "app JWT"
		if strings.HasPrefix(path, "/admin/") {
			access = "staff JWT: " + strings.Join(roles, ", ")
		}
		if strings.HasPrefix(path, "/files/") {
			access = "owner driver JWT or active staff JWT"
		}
		if strings.HasPrefix(path, "/webhooks/") {
			access = "provider HMAC"
		}
	}
	guard, status = "Resource scope needs an endpoint-specific review.", "review"
	switch {
	case path == "/driver/profile/:userId":
		guard, status = "Other app users receive name, vehicle type, picture and rating only; matching driver owner receives the full profile. Staff full profile access uses the audited admin route.", "public DTO; owner scoped"
	case path == "/passenger/profile/:userId":
		guard, status = "Other app users receive name, picture and rating only; matching passenger owner receives the full profile. Phone is excluded from the public DTO.", "public DTO; owner scoped"
	case path == "/admin/users/:id/profile":
		guard, status = "StaffProtected plus support/admin role; request audit log is written by the admin route group.", "staff scoped and audited"
	case path == "/driver/documents/:id/confirm":
		guard, status = "Driver identity and document driver_id match; UUID validated; pending state, expiry, size and content validation before submission.", "owner scoped"
	case path == "/files/documents/:id":
		guard, status = "Authentication before lookup; owning active driver or active staff; pending files denied; every staff read audited; private signed GET or local bytes.", "owner / staff scoped"
	case path == "/files/licences/:id":
		guard, status = "Legacy filename/profile lookup resolves owning driver; active app driver must match owner; active staff may read with audit.", "legacy owner / staff scoped"
	case path == "/profile-images/:id":
		guard, status = "UUID resolves only a stored public profile image key and redirects to configured public domain.", "intentional public image"
	case strings.HasPrefix(path, "/uploads/"):
		guard, status = "Static public upload directory only; B2 one-shot migration quarantines unknown/private legacy files.", "intentional public image"
	case path == "/ratings/user/:userId":
		guard, status = "User existence checked; any app user may view the rating list for another account.", "intentional app rating lookup"
	case path == "/blocks/:userId":
		guard, status = "Unblock query scopes blocker_id to authenticated user; supplied userId is the blocked target.", "owner scoped"
	case path == "/payments/:id":
		guard, status = "Payment query requires payer_id or driver_id equal to authenticated account.", "participant scoped"
	case path == "/driver/payouts/:id":
		guard, status = "Driver role; cancellation store checks driver_id for mutable requested payout; repeated terminal-state cancellation returns its existing no-op result.", "owner scoped mutation"
	case path == "/emergency-contacts/:id":
		guard, status = "UPDATE/DELETE include contact id and authenticated user_id.", "owner scoped"
	case path == "/sos/:id" || path == "/sos/:id/:action":
		guard, status = "Existing alert lookup scopes triggered_by to authenticated account.", "triggering account scoped"
	case path == "/rides/:rideId/sos":
		guard, status = "Store resolves ride through RideForUser and validates SOS state before creating alert.", "ride participant scoped"
	case path == "/rides/:rideId/share":
		guard, status = "RideForUser requires passenger/driver participation; share contacts belong to caller; stop-sharing only deletes tokens created by caller.", "ride participant scoped"
	case path == "/rides/:rideId/pay":
		guard, status = "Passenger role; RideForUser and passenger_id require caller to own a completed passenger ride; idempotency header required.", "passenger scoped"
	case path == "/rides/:rideId/location":
		guard, status = "RideForUser plus assigned driver_id and in_progress status; valid coordinates and per-ride rate limit.", "assigned driver scoped"
	case path == "/rides/:id/rating":
		guard, status = "Caller must be a participant in a completed ride; unique ride_id and rater constraint allows one rating per ride participant.", "completed ride participant scoped"
	case path == "/rides" && route.Method == "POST":
		guard, status = "Passenger role; idempotency key; server-calculated fare; serialized active ride check and transactional event creation.", "passenger scoped and idempotent"
	case path == "/rides" && route.Method == "GET", path == "/rides/:id":
		guard, status = "Authenticated participant query includes passenger_id or assigned driver_id; other users receive 404.", "ride participant scoped"
	case path == "/rides/:id/cancel":
		guard, status = "Passenger or assigned driver only; legal state and cancellation reason checked in the same transaction as event and idempotency record.", "ride participant scoped and idempotent"
	case path == "/rides/:id/accept", path == "/rides/:id/decline", path == "/rides/:id/arrived", path == "/rides/:id/start", path == "/rides/:id/complete":
		guard, status = "Driver role; current offer/assignment and eligibility rechecked under row lock; state, event and idempotency key commit atomically.", "assigned driver scoped and idempotent"
	case path == "/driver/offers":
		guard, status = "Driver role; only unexpired offers assigned to the caller are returned.", "assigned driver scoped"
	case path == "/driver/availability":
		guard, status = "Driver role; going online requires current approval/documents and no active ride.", "owner scoped and idempotent"
	case path == "/driver/location":
		guard, status = "Online driver role; bounded coordinates are stored by driver_id with an idempotency key.", "owner scoped and idempotent"
	case path == "/admin/rides/:id" || path == "/admin/rides/:id/cancel" || path == "/admin/rides/:id/reassign":
		guard, status = "Support/admin staff; ride is loaded from persistence, operational changes require reasons and write timeline events; staff requests are audit logged.", "staff scoped and audited"
	case path == "/admin/tickets/:id":
		guard, status = "Support/admin staff; persisted ticket ownership, assignment, status and resolution are checked; notes and changes are retained and audited.", "staff scoped and audited"
	case path == "/admin/ratings/:id/hide" || path == "/admin/ratings/:id/restore":
		guard, status = "Admin-only moderation; visibility changes recompute public rating aggregates and are audit logged.", "admin scoped and audited"
	case path == "/share/:token":
		guard, status = "Rate limited capability token is hashed, expiry checked; current public share DTO restricts exposed trip fields.", "intentional capability access"
	case path == "/incidents/files/:id":
		guard, status = "Attachment query includes authenticated user_id; local file sent from existing attachment metadata.", "owner scoped; legacy local file"
	case path == "/incidents/:id/attachments":
		guard, status = "Incident query includes reporter_id equal to authenticated user; attachment limit and local upload validation.", "reporter scoped"
	case path == "/account/export-requests/:id/download":
		guard, status = "Ready, unexpired request id plus caller user_id; private export path check; download counter and audit recorded transactionally.", "owner scoped"
	case path == "/auth/sessions/:id":
		guard, status = "AppProtected validates the active session; revocation query scopes the session id to the authenticated user.", "owner scoped"
	case strings.HasPrefix(path, "/webhooks/payments/"):
		guard, status = "Configured provider match, HMAC timestamp/signature verification of raw body before event processing.", "provider authenticated"
	case path == "/admin/drivers/:id/verify":
		guard, status = "Staff support/admin route check; handler always returns 410.", "retired"
	case path == "/admin/drivers/:id/documents/:documentId/reviews":
		guard, status = "Staff route role; document lookup includes id and driver_id; store enforces reviewer claim/policy and persists audited decision.", "staff scope; nested id checked"
	case path == "/admin/users/:id/notes/:noteId":
		guard, status = "Admin role; deletion query includes both note id and target user id.", "admin scope; nested id checked"
	case strings.HasPrefix(path, "/admin/"):
		guard, status = "StaffProtected and listed route roles authorize cross-user staff access; controller/store enforce action policy and audit.", "staff scope"
	}
	return access, guard, status
}
