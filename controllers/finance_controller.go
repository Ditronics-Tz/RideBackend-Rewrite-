package controllers

import (
	"bufio"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/gofiber/fiber/v2"
	"log"
	"net/http"
	"ride-backend/middleware"
	"ride-backend/models"
	"ride-backend/store"
	"strconv"
	"strings"
	"time"
)

func (oc *OperationsController) Wallet(c *fiber.Ctx) error {
	uid, _ := middleware.CurrentUser(c)
	if middleware.CurrentStaff(c) != nil {
		uid = c.Params("userId")
	}
	w, err := oc.Store.GetWallet(uid)
	if err != nil {
		return operationError(c, err)
	}
	if !strings.HasSuffix(c.Path(), "/ledger") && middleware.CurrentStaff(c) == nil {
		return c.JSON(w)
	}
	p, err := operationPage(c)
	if err != nil {
		return operationError(c, err)
	}
	entries := []models.LedgerEntry{}
	total, err := oc.Store.PageRows(oc.Store.DB().Model(&models.LedgerEntry{}).Where("wallet_id = ?", w.ID), p, &entries)
	if err != nil {
		return operationError(c, err)
	}
	return c.JSON(fiber.Map{"wallet": w, "ledger": entries, "total": total, "limit": p.Limit, "offset": p.Offset})
}
func (oc *OperationsController) Pay(c *fiber.Ctx) error {
	var in store.PayInput
	if err := c.BodyParser(&in); err != nil {
		return operationError(c, store.FieldError("body", "invalid payment request"))
	}
	uid, _ := middleware.CurrentUser(c)
	p, err := oc.Store.InitiatePayment(uid, c.Params("rideId"), c.Get("Idempotency-Key"), in)
	if err != nil {
		return operationError(c, err)
	}
	return c.Status(202).JSON(p)
}
func (oc *OperationsController) Payments(c *fiber.Ctx) error {
	p, err := operationPage(c)
	if err != nil {
		return operationError(c, err)
	}
	q := oc.Store.DB().Model(&models.PaymentTransaction{})
	if middleware.CurrentStaff(c) == nil {
		uid, _ := middleware.CurrentUser(c)
		q = q.Where("payer_id = ? OR driver_id = ?", uid, uid)
	} else {
		for col, v := range map[string]string{"status": c.Query("status"), "method": c.Query("method"), "ride_id": c.Query("ride"), "provider_ref": c.Query("provider_ref")} {
			if v != "" {
				q = q.Where(col+" = ?", v)
			}
		}
		if c.Query("user") != "" {
			q = q.Where("payer_id = ? OR driver_id = ?", c.Query("user"), c.Query("user"))
		}
		for name, op := range map[string]string{"min_amount": ">=", "max_amount": "<="} {
			if c.Query(name) != "" {
				v, err := strconv.ParseInt(c.Query(name), 10, 64)
				if err != nil || v < 0 {
					return operationError(c, store.FieldError(name, "amount must be nonnegative whole TZS"))
				}
				q = q.Where("amount "+op+" ?", v)
			}
		}
	}
	for name, op := range map[string]string{"from": ">=", "to": "<"} {
		if c.Query(name) != "" {
			date, err := time.Parse(time.RFC3339, c.Query(name))
			if err != nil {
				return operationError(c, store.FieldError(name, "date must be RFC3339"))
			}
			q = q.Where("created_at "+op+" ?", date)
		}
	}
	rows := []models.PaymentTransaction{}
	total, err := oc.Store.PageRows(q, p, &rows)
	if err != nil {
		return operationError(c, err)
	}
	return c.JSON(fiber.Map{"payments": rows, "total": total, "limit": p.Limit, "offset": p.Offset})
}
func (oc *OperationsController) PaymentDetail(c *fiber.Ctx) error {
	q := oc.Store.DB().Where("id = ?", c.Params("id"))
	staff := middleware.CurrentStaff(c)
	if staff == nil {
		uid, _ := middleware.CurrentUser(c)
		q = q.Where("payer_id = ? OR driver_id = ?", uid, uid)
	}
	var payment models.PaymentTransaction
	if err := q.First(&payment).Error; err != nil {
		return operationError(c, err)
	}
	if staff == nil {
		return c.JSON(payment)
	}
	p, err := operationPage(c)
	if err != nil {
		return operationError(c, err)
	}
	entries := []models.LedgerEntry{}
	entriesTotal, err := oc.Store.PageRows(oc.Store.DB().Model(&models.LedgerEntry{}).Where("ref_id = ? OR ref_id IN (?)", payment.ID, oc.Store.DB().Model(&models.Refund{}).Select("id").Where("transaction_id = ?", payment.ID)), p, &entries)
	if err != nil {
		return operationError(c, err)
	}
	events := []models.WebhookEvent{}
	eventsTotal, err := oc.Store.PageRows(oc.Store.DB().Model(&models.WebhookEvent{}).Where("reference IN ?", []string{payment.ID, payment.ProviderRef}), p, &events)
	if err != nil {
		return operationError(c, err)
	}
	return c.JSON(fiber.Map{"payment": payment, "ledger": entries, "ledger_total": entriesTotal, "webhooks": events, "webhooks_total": eventsTotal})
}
func (oc *OperationsController) PaymentWebhook(c *fiber.Ctx) error {
	provider := c.Params("provider")
	if oc.Store.PaymentProvider == nil || provider != oc.Cfg.PaymentProviderName {
		return operationError(c, store.OpError(404, "unknown provider"))
	}
	headers := http.Header{}
	headers.Set("X-Payment-Signature", c.Get("X-Payment-Signature"))
	headers.Set("X-Payment-Timestamp", c.Get("X-Payment-Timestamp"))
	if err := oc.Store.PaymentProvider.VerifyWebhook(headers, c.Body()); err != nil {
		return operationError(c, store.OpError(401, "invalid webhook signature"))
	}
	var event store.PaymentEvent
	if err := json.Unmarshal(c.Body(), &event); err != nil {
		return operationError(c, store.FieldError("body", "invalid webhook"))
	}
	if err := oc.Store.ProcessPaymentEvent(provider, event, utilsHash(c.Body())); err != nil {
		return operationError(c, err)
	}
	return c.JSON(fiber.Map{"received": true})
}
func (oc *OperationsController) Payouts(c *fiber.Ctx) error {
	uid, _ := middleware.CurrentUser(c)
	staff := middleware.CurrentStaff(c)
	if c.Method() == "POST" {
		var in struct {
			Amount int64  `json:"amount"`
			Method string `json:"method"`
			Phone  string `json:"phone"`
		}
		if err := c.BodyParser(&in); err != nil {
			return operationError(c, store.FieldError("body", "invalid payout"))
		}
		p, err := oc.Store.RequestPayout(uid, c.Get("Idempotency-Key"), in.Method, in.Phone, in.Amount)
		if err != nil {
			return operationError(c, err)
		}
		return c.Status(201).JSON(p)
	}
	if c.Method() == "DELETE" {
		if err := oc.Store.WorkPayout(c.Params("id"), "cancel", "", uid, ""); err != nil {
			return operationError(c, err)
		}
		return c.SendStatus(204)
	}
	p, err := operationPage(c)
	if err != nil {
		return operationError(c, err)
	}
	q := oc.Store.DB().Model(&models.DriverPayout{})
	if staff == nil {
		q = q.Where("driver_id = ?", uid)
	}
	if c.Query("status") != "" {
		q = q.Where("status = ?", c.Query("status"))
	}
	rows := []models.DriverPayout{}
	total, err := oc.Store.PageRows(q, p, &rows)
	if err != nil {
		return operationError(c, err)
	}
	if staff != nil {
		out := []fiber.Map{}
		for _, row := range rows {
			party, err := oc.Store.Party(row.DriverID)
			if err != nil {
				return operationError(c, err)
			}
			var profile models.DriverProfile
			if err := oc.Store.DB().First(&profile, "user_id = ?", row.DriverID).Error; err != nil {
				return operationError(c, err)
			}
			wallet, err := oc.Store.GetWallet(row.DriverID)
			if err != nil {
				return operationError(c, err)
			}
			held, err := oc.Store.HasFraudMitigation(row.DriverID, "payout_hold")
			if err != nil {
				return operationError(c, err)
			}
			encoded, err := json.Marshal(row)
			if err != nil {
				return operationError(c, err)
			}
			view := fiber.Map{}
			if err := json.Unmarshal(encoded, &view); err != nil {
				return operationError(c, err)
			}
			view["driver_name"] = party.Name
			view["verified"] = profile.VerificationStatus == models.Approved
			view["fraud_hold"] = held
			view["wallet_balance"] = wallet.Balance
			view["wallet_held"] = wallet.Held
			out = append(out, view)
		}
		return c.JSON(fiber.Map{"payouts": out, "total": total, "limit": p.Limit, "offset": p.Offset})
	}
	return c.JSON(fiber.Map{"payouts": rows, "total": total, "limit": p.Limit, "offset": p.Offset})
}
func (oc *OperationsController) PayoutAction(c *fiber.Ctx) error {
	var in struct {
		Reason string   `json:"reason"`
		IDs    []string `json:"ids"`
	}
	if len(c.Body()) > 0 {
		if err := c.BodyParser(&in); err != nil {
			return operationError(c, store.FieldError("body", "invalid payout action"))
		}
	}
	actor := middleware.CurrentStaff(c)
	action := c.Params("action")
	if strings.HasSuffix(c.Path(), "/batch-approve") {
		action = "batch-approve"
	}
	if action == "batch-approve" {
		if len(in.IDs) < 1 || len(in.IDs) > 100 {
			return operationError(c, store.FieldError("ids", "select 1-100 payouts"))
		}
		seen := map[string]bool{}
		for _, id := range in.IDs {
			if seen[id] {
				return operationError(c, store.FieldError("ids", "duplicate payout id"))
			}
			seen[id] = true
		}
		var total int64
		err := oc.Store.Transaction(func(tx *store.Store) error {
			for _, id := range in.IDs {
				var p models.DriverPayout
				if err := tx.DB().First(&p, "id = ?", id).Error; err != nil {
					return err
				}
				total += p.Amount
				if err := tx.WorkPayout(id, "approve", actor.ID, "", ""); err != nil {
					return err
				}
			}
			return tx.CreateAuditLog(actor.ID, "payout_batch_approved", "payout", "batch", c.IP(), fiber.Map{"total": total, "count": len(in.IDs)})
		})
		if err != nil {
			return operationError(c, err)
		}
		return c.JSON(fiber.Map{"approved": len(in.IDs), "total": total})
	}
	if err := oc.Store.WorkPayout(c.Params("id"), action, actor.ID, "", in.Reason); err != nil {
		return operationError(c, err)
	}
	return c.JSON(fiber.Map{"id": c.Params("id"), "action": action})
}
func (oc *OperationsController) RefundRequest(c *fiber.Ctx) error {
	var in struct {
		Amount int64  `json:"amount"`
		Reason string `json:"reason"`
	}
	if err := c.BodyParser(&in); err != nil {
		return operationError(c, store.FieldError("body", "invalid refund"))
	}
	r, err := oc.Store.RequestRefund(c.Params("id"), c.Get("Idempotency-Key"), in.Reason, in.Amount, middleware.CurrentStaff(c))
	if err != nil {
		return operationError(c, err)
	}
	return c.Status(201).JSON(r)
}
func (oc *OperationsController) Refunds(c *fiber.Ctx) error {
	p, err := operationPage(c)
	if err != nil {
		return operationError(c, err)
	}
	q := oc.Store.DB().Model(&models.Refund{})
	if c.Query("status") != "" {
		q = q.Where("status = ?", c.Query("status"))
	}
	rows := []models.Refund{}
	total, err := oc.Store.PageRows(q, p, &rows)
	if err != nil {
		return operationError(c, err)
	}
	settings, err := oc.Store.GetPaymentSettings()
	if err != nil {
		return operationError(c, err)
	}
	return c.JSON(fiber.Map{"refunds": rows, "total": total, "limit": p.Limit, "offset": p.Offset, "support_limit": settings.RefundSupportLimit})
}
func (oc *OperationsController) RefundAction(c *fiber.Ctx) error {
	var in struct {
		Reason string `json:"reason"`
	}
	if len(c.Body()) > 0 {
		if err := c.BodyParser(&in); err != nil {
			return operationError(c, store.FieldError("body", "invalid refund action"))
		}
	}
	if err := oc.Store.WorkRefund(c.Params("id"), c.Params("action"), in.Reason, middleware.CurrentStaff(c)); err != nil {
		return operationError(c, err)
	}
	return c.JSON(fiber.Map{"id": c.Params("id")})
}
func (oc *OperationsController) AdjustWallet(c *fiber.Ctx) error {
	var in struct {
		Amount int64  `json:"amount"`
		Reason string `json:"reason"`
	}
	if err := c.BodyParser(&in); err != nil {
		return operationError(c, store.FieldError("body", "invalid adjustment"))
	}
	if err := oc.Store.AdjustWallet(c.Params("userId"), middleware.CurrentStaff(c).ID, c.Get("Idempotency-Key"), in.Reason, in.Amount); err != nil {
		return operationError(c, err)
	}
	return oc.Wallet(c)
}
func (oc *OperationsController) PaymentSettings(c *fiber.Ctx) error {
	if c.Method() == "PUT" {
		var in struct {
			models.PaymentSettings
			CommissionRules []models.CommissionRule `json:"commission_rules"`
		}
		if err := c.BodyParser(&in); err != nil {
			return operationError(c, store.FieldError("body", "invalid settings"))
		}
		if err := oc.Store.UpdatePaymentSettings(middleware.CurrentStaff(c).ID, in.PaymentSettings, in.CommissionRules); err != nil {
			return operationError(c, err)
		}
	}
	settings, err := oc.Store.GetPaymentSettings()
	if err != nil {
		return operationError(c, err)
	}
	p, err := operationPage(c)
	if err != nil {
		return operationError(c, err)
	}
	rules := []models.CommissionRule{}
	total, err := oc.Store.PageRows(oc.Store.DB().Model(&models.CommissionRule{}), p, &rules)
	if err != nil {
		return operationError(c, err)
	}
	history := []models.PaymentSettings{}
	historyTotal, err := oc.Store.PageRows(oc.Store.DB().Model(&models.PaymentSettings{}), p, &history)
	if err != nil {
		return operationError(c, err)
	}
	return c.JSON(fiber.Map{"settings": settings, "commission_rules": rules, "total": total, "history": history, "history_total": historyTotal, "limit": p.Limit, "offset": p.Offset})
}
func (oc *OperationsController) Reconciliation(c *fiber.Ctx) error {
	p, err := operationPage(c)
	if err != nil {
		return operationError(c, err)
	}
	if c.Method() == "POST" {
		if c.Params("id") != "" {
			var in struct {
				Note string `json:"note"`
			}
			if err := c.BodyParser(&in); err != nil || strings.TrimSpace(in.Note) == "" {
				return operationError(c, store.FieldError("note", "resolution note required"))
			}
			err := oc.Store.Transaction(func(tx *store.Store) error {
				r := tx.DB().Model(&models.ReconciliationException{}).Where("id = ? AND status = ?", c.Params("id"), "open").Updates(map[string]any{"status": "resolved", "note": in.Note})
				if r.Error != nil {
					return r.Error
				}
				if r.RowsAffected == 0 {
					return store.OpError(409, "exception is not open")
				}
				return tx.CreateAuditLog(middleware.CurrentStaff(c).ID, "resolve_reconciliation_exception", "exception", c.Params("id"), c.IP(), nil)
			})
			if err != nil {
				return operationError(c, err)
			}
			return c.JSON(fiber.Map{"status": "resolved"})
		}
		var run *models.ReconciliationRun
		err := oc.Store.Transaction(func(tx *store.Store) error {
			var err error
			run, err = tx.Reconcile("manual:" + c.Get("Idempotency-Key"))
			if err != nil {
				return err
			}
			return tx.CreateAuditLog(middleware.CurrentStaff(c).ID, "reconciliation_requested", "reconciliation", run.ID, c.IP(), nil)
		})
		if err != nil {
			return operationError(c, err)
		}
		return c.Status(202).JSON(run)
	}
	if strings.HasSuffix(c.Path(), "/exceptions") {
		rows := []models.ReconciliationException{}
		q := oc.Store.DB().Model(&models.ReconciliationException{})
		if c.Query("run") != "" {
			q = q.Where("run_id = ?", c.Query("run"))
		}
		if c.Query("status") != "" {
			q = q.Where("status = ?", c.Query("status"))
		}
		total, err := oc.Store.PageRows(q, p, &rows)
		if err != nil {
			return operationError(c, err)
		}
		return c.JSON(fiber.Map{"exceptions": rows, "total": total, "limit": p.Limit, "offset": p.Offset})
	}
	rows := []models.ReconciliationRun{}
	total, err := oc.Store.PageRows(oc.Store.DB().Model(&models.ReconciliationRun{}), p, &rows)
	if err != nil {
		return operationError(c, err)
	}
	return c.JSON(fiber.Map{"runs": rows, "total": total, "limit": p.Limit, "offset": p.Offset})
}
func (oc *OperationsController) Earnings(c *fiber.Ctx) error {
	uid, _ := middleware.CurrentUser(c)
	now := time.Now().UTC()
	out := fiber.Map{}
	for _, span := range []struct {
		name  string
		since time.Time
	}{{"day", now.Add(-24 * time.Hour)}, {"week", now.Add(-7 * 24 * time.Hour)}} {
		var row struct {
			Fare       int64 `json:"fare"`
			Commission int64 `json:"commission"`
			CashOwed   int64 `json:"cash_owed"`
		}
		if err := oc.Store.DB().Model(&models.PaymentTransaction{}).Where("driver_id = ? AND status IN ? AND created_at >= ?", uid, []string{"succeeded", "partially_refunded", "refunded"}, span.since).Select("COALESCE(SUM(fare),0) AS fare, COALESCE(SUM(commission),0) AS commission, COALESCE(SUM(CASE WHEN method = 'cash' THEN commission-discount_amount ELSE 0 END),0) AS cash_owed").Scan(&row).Error; err != nil {
			return operationError(c, err)
		}
		out[span.name] = row
	}
	return c.JSON(out)
}
func (oc *OperationsController) FinanceSummary(c *fiber.Ctx) error {
	var totals struct {
		GMV        int64 `json:"gmv"`
		Commission int64 `json:"commission"`
	}
	if err := oc.Store.DB().Model(&models.PaymentTransaction{}).Where("ride_id IS NOT NULL AND status IN ?", []string{"succeeded", "partially_refunded", "refunded"}).Select("COALESCE(SUM(fare),0) AS gmv,COALESCE(SUM(commission),0) AS commission").Scan(&totals).Error; err != nil {
		return operationError(c, err)
	}
	var payouts, refunds int64
	if err := oc.Store.DB().Model(&models.DriverPayout{}).Where("status IN ?", []string{"requested", "approved", "processing"}).Select("COALESCE(SUM(amount),0)").Scan(&payouts).Error; err != nil {
		return operationError(c, err)
	}
	if err := oc.Store.DB().Model(&models.Refund{}).Where("status = ?", "processed").Select("COALESCE(SUM(amount),0)").Scan(&refunds).Error; err != nil {
		return operationError(c, err)
	}
	daily := []struct {
		Date       string `json:"date"`
		GMV        int64  `json:"gmv"`
		Commission int64  `json:"commission"`
	}{}
	if err := oc.Store.DB().Model(&models.PaymentTransaction{}).Where("ride_id IS NOT NULL AND status IN ? AND created_at >= ?", []string{"succeeded", "partially_refunded", "refunded"}, time.Now().UTC().Add(-30*24*time.Hour)).Select("DATE(created_at) AS date,SUM(fare) AS gmv,SUM(commission) AS commission").Group("DATE(created_at)").Order("DATE(created_at) ASC").Scan(&daily).Error; err != nil {
		return operationError(c, err)
	}
	return c.JSON(fiber.Map{"gmv": totals.GMV, "commission": totals.Commission, "pending_payouts": payouts, "refunds": refunds, "daily": daily})
}
func (oc *OperationsController) FinanceCSV(c *fiber.Ctx) error {
	if err := oc.Store.CreateAuditLog(middleware.CurrentStaff(c).ID, "export_payments", "payment", "collection", c.IP(), nil); err != nil {
		return operationError(c, err)
	}
	c.Set("Content-Type", "text/csv")
	c.Set("Content-Disposition", "attachment; filename=payments.csv")
	c.Set("Cache-Control", "no-store")
	s := oc.Store
	c.Context().SetBodyStreamWriter(func(w *bufio.Writer) {
		csvWriter := csv.NewWriter(w)
		if err := csvWriter.Write([]string{"id", "ride_id", "method", "amount_tzs", "status", "created_at"}); err != nil {
			return
		}
		lastID := ""
		for {
			rows := []models.PaymentTransaction{}
			q := s.DB().Order("id ASC").Limit(100)
			if lastID != "" {
				q = q.Where("id > ?", lastID)
			}
			if err := q.Find(&rows).Error; err != nil {
				log.Print("Payment CSV database read failed; stream terminated")
				return
			}
			for _, p := range rows {
				ride := ""
				if p.RideID != nil {
					ride = *p.RideID
				}
				if err := csvWriter.Write([]string{p.ID, ride, p.Method, fmt.Sprint(p.Amount), p.Status, p.CreatedAt.Format(time.RFC3339)}); err != nil {
					return
				}
				lastID = p.ID
			}
			csvWriter.Flush()
			if csvWriter.Error() != nil {
				return
			}
			if len(rows) < 100 {
				return
			}
		}
	})
	return nil
}

func utilsHash(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }
