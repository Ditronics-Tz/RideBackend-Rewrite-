# Dual ride modes: rollout and recovery

The application implementation is local; production deployment and physical-device acceptance are separate. Apply additive migrations through **16** before starting this backend. Do not rewrite migrations 1–15 or drop populated transport/ledger tables during rollback.

## Configuration and compatibility

An administrator must choose the shared fee (TZS 500–1,000 inclusive), hail percentage (integer 0–100), and whether to accept new shared bookings. No production shared fee is selected automatically. The fixture server's TZS 700 and 12% are test values only. Settings updates use optimistic version checks and retain staff audit history. Existing hail clients keep their routes; request-time fee snapshots protect already established trips.

Drivers declare passenger capacity, excluding the driver, in vehicle setup. The operational input bound is 1–100 seats; bajaji/car variants are eligible and legacy boda is retained for hailing. Unknown capacity blocks publishing. Material capacity or vehicle changes require document review and cannot change an active commitment.

## Initial policies

One account reserves one seat, with one common pickup and destination. Publication fixes route, seat price, capacity and fee version. Holds last five minutes and are offered only while a full hold fits before the 6–180 minute waiting deadline. A booking is confirmed only by the owning driver's submission of the passenger's six-digit PIN; five wrong attempts release it. PINs never appear in a manifest, public event or URL.

A waiting deadline expires an empty trip, but preserves confirmed passengers for explicit departure. Start requires at least one confirmed passenger and no live unverified holds. Cancellation is a pre-departure operation with a reason; in-progress cancellation is not exposed as an ordinary action. New publication and allocation are blocked when another incompatible hail/shared commitment exists.

Cash fares go directly to the driver. Each completed shared trip assesses exactly one fixed whole-trip fee, regardless of passenger count. Low fare revenue does not cap that fee; a negative illustrative net is disclosed before publication. Hails use the gross authoritative fare before promotions, integer-percent half-up whole-TZS rounding, and the request-time rule version. Promotion subsidies keep balanced ledger entries.

Assessments and collection are distinct. Completion creates balanced receivable/revenue entries once. Collection debits the driver wallet within the configured floor and clears the receivable. A frozen/insufficient wallet leaves a completed trip with an outstanding receivable; it does not strand the journey in progress. The dispatch worker retries collection and outstanding fees restrict new commitments. Use supported wallet adjustments/funding; never delete or edit immutable ledger rows.

## Release sequence

1. Back up the intended database using the established operator process, then apply migration 16 explicitly using that environment's injected configuration. The migration command does not load `.env`.
2. Deploy this backend with new sharing disabled. Verify health/readiness and schema 16.
3. Deploy the matching dashboard and both compatible Android builds. Default Android artifacts use the configured HTTPS host and live driver repository.
4. Review registered vehicles and choose fees in transport settings; enable only an authorized pilot.
5. Exercise sharing and hail journeys, expiry, incorrect PINs, reconnect/process death, stale/denied GPS, background tracking and settlement on real devices. The owner elected to run these device checks.
6. Inspect `/admin/transport/assessments`, shared-trip staff timelines, finance totals, and shared SOS/support references before expanding availability.

## Workers, delivery and observability

The existing ride-dispatch worker materializes shared expirations and retries receivable collection in bounded batches. Reads and commands still enforce expiry if the worker is stopped. Long polls read a durable transactional invalidation outbox directly across instances; clients retain the cursor, discard older snapshot versions, and refetch after events/reset. Polls are bounded to 1.5 seconds and rate-limited. Public events contain no PINs, passenger identity or cancellation free text.

Lock order is transport cursor → actor → profile/trip → seat → wallet. The coarse database guard orders commits and prevents mixed-mode deadlocks; benchmark contention before a large fleet rollout. A local 20-store-instance fanout scenario is recorded in the shared verification report; it is not a production capacity claim.

Monitor expired-held rows/worker failures, invalid-pickup attempts, new-booking conflicts, pending `transport_assessments` with `status='receivable'`, wallet restrictions, and event cursor/reconnect latency. Request logs must not include PINs or credentials. Event audit reasons are staff-only. Financial totals include new assessments plus legacy settlements once.

## Disable, recovery and rollback

Turning off sharing prevents new publication/reservation but retains read, release, verify, departure, completion, history and support for active participants. Outbox state is persisted; restart another API instance and resume from the cursor. There is no broker acknowledgment to reset. Keep event history until an explicit retention/archive policy is approved.

Roll back to a schema-16-compatible binary that can drain active commitments, or deploy a forward correction. An older binary that insists on schema 15 will refuse startup. Preserve active trips and immutable assessments; investigate anomalies through the staff timeline and use supported financial adjustments.

## Owner-run local device acceptance

`go run ./cmd/transport-acceptance` requires an explicit disposable `RIDA_ACCEPTANCE_DSN` whose database name starts `rida_acceptance_`. It binds only `127.0.0.1:3005`, does not read application `.env`, and seeds clearly named test accounts. It is a separate executable, not a production route. Use a newly created database after changing local development migrations.

The instrumentation tests use `driver@acceptance.test` and `passenger@acceptance.test` with the fixture password declared in that command. They call a real PostgreSQL-backed HTTP API and normal navigation. They do not mock successful rides. For an emulator, compile with:

```bash
./gradlew :app:connectedDebugAndroidTest \
  -Prida.apiBaseUrl=http://10.0.2.2:3005/api/v1/ \
  -Prida.allowInsecureLocalApi=true
```

For a USB device, use `adb reverse tcp:3005 tcp:3005` and the localhost debug base URL. The instrumentation accepts those local hosts; the manual checklist is also available. HTTP is accepted only with explicit local debug opt-in and is rejected for release tasks. Build ordinary HTTPS APKs again before distributing outside local testing.
