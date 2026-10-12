package controllers

import (
	"encoding/json"
	"github.com/gofiber/fiber/v2"
	"ride-backend/middleware"
	"ride-backend/models"
	"ride-backend/store"
	"sort"
	"time"
)

func (oc *OperationsController) RideLocation(c *fiber.Ctx) error {
	uid, _ := middleware.CurrentUser(c)
	var in store.SOSInput
	if err := c.BodyParser(&in); err != nil {
		return operationError(c, store.FieldError("location", "invalid location"))
	}
	if err := oc.Store.RecordRideLocation(uid, c.Params("rideId"), in.Lat, in.Lng, in.Accuracy); err != nil {
		return operationError(c, err)
	}
	return c.SendStatus(204)
}
func (oc *OperationsController) FraudFlags(c *fiber.Ctx) error {
	p, err := operationPage(c)
	if err != nil {
		return operationError(c, err)
	}
	q := oc.Store.DB().Model(&models.FraudFlag{})
	if after := c.Query("from"); after != "" {
		ts, err := time.Parse(time.RFC3339, after)
		if err != nil {
			return operationError(c, store.FieldError("from", "from must be RFC3339"))
		}
		q = q.Where("created_at >= ?", ts)
	}
	switch c.Query("hold") {
	case "active":
		q = q.Where("id IN (SELECT flag_id FROM fraud_mitigations WHERE active = ?)", true)
	case "none":
		q = q.Where("id NOT IN (SELECT flag_id FROM fraud_mitigations WHERE active = ?)", true)
	case "":
	default:
		return operationError(c, store.FieldError("hold", "hold must be active or none"))
	}
	for col, v := range map[string]string{"status": c.Query("status"), "severity": c.Query("severity"), "type": c.Query("type"), "assigned_to": c.Query("assignee"), "user_id": c.Query("user")} {
		if v != "" {
			q = q.Where(col+" = ?", v)
		}
	}
	rows := []models.FraudFlag{}
	total, err := oc.Store.PageRows(q, p, &rows)
	if err != nil {
		return operationError(c, err)
	}
	holds := []models.FraudMitigation{}
	ids := []string{}
	for _, f := range rows {
		ids = append(ids, f.ID)
	}
	if len(ids) > 0 {
		if err := oc.Store.DB().Where("flag_id IN ? AND active = ?", ids, true).Find(&holds).Error; err != nil {
			return operationError(c, err)
		}
	}
	return c.JSON(fiber.Map{"flags": rows, "holds": holds, "total": total, "limit": p.Limit, "offset": p.Offset})
}
func (oc *OperationsController) FraudDetail(c *fiber.Ctx) error {
	p, err := operationPage(c)
	if err != nil {
		return operationError(c, err)
	}
	var flag models.FraudFlag
	if err := oc.Store.DB().First(&flag, "id = ?", c.Params("id")).Error; err != nil {
		return operationError(c, err)
	}
	ids := []string{}
	if err := json.Unmarshal([]byte(flag.RelatedUserIDs), &ids); err != nil {
		return operationError(c, err)
	}
	linked := []store.PartySummary{}
	for _, id := range ids {
		party, err := oc.Store.Party(id)
		if err != nil {
			return operationError(c, err)
		}
		linked = append(linked, *party)
	}
	notes := []models.FraudNote{}
	total, err := oc.Store.PageRows(oc.Store.DB().Model(&models.FraudNote{}).Where("flag_id = ?", flag.ID), p, &notes)
	if err != nil {
		return operationError(c, err)
	}
	actions := []models.FraudAction{}
	actionsTotal, err := oc.Store.PageRows(oc.Store.DB().Model(&models.FraudAction{}).Where("flag_id = ?", flag.ID), p, &actions)
	if err != nil {
		return operationError(c, err)
	}
	otherFlags := []models.FraudFlag{}
	relatedTotal, err := oc.Store.PageRows(oc.Store.DB().Model(&models.FraudFlag{}).Where("user_id = ? AND id <> ?", flag.UserID, flag.ID), p, &otherFlags)
	if err != nil {
		return operationError(c, err)
	}
	holds := []models.FraudMitigation{}
	if err := oc.Store.DB().Where("flag_id = ? AND active = ?", flag.ID, true).Find(&holds).Error; err != nil {
		return operationError(c, err)
	}
	user, err := oc.Store.Party(flag.UserID)
	if err != nil {
		return operationError(c, err)
	}
	return c.JSON(fiber.Map{"flag": flag, "user": user, "linked_accounts": linked, "related_flags": otherFlags, "related_total": relatedTotal, "notes": notes, "notes_total": total, "actions": actions, "actions_total": actionsTotal, "holds": holds})
}
func (oc *OperationsController) FraudAction(c *fiber.Ctx) error {
	var in store.FraudWork
	if len(c.Body()) > 0 {
		if err := c.BodyParser(&in); err != nil {
			return operationError(c, store.FieldError("body", "invalid fraud action"))
		}
	}
	if err := oc.Store.WorkFraud(c.Params("id"), c.Params("action"), middleware.CurrentStaff(c), in); err != nil {
		return operationError(c, err)
	}
	return c.JSON(fiber.Map{"id": c.Params("id"), "action": c.Params("action")})
}
func (oc *OperationsController) FraudRules(c *fiber.Ctx) error {
	if c.Method() == "PUT" {
		var in struct {
			Rules []models.FraudRule `json:"rules"`
		}
		if err := c.BodyParser(&in); err != nil {
			return operationError(c, store.FieldError("rules", "invalid rule settings"))
		}
		if len(in.Rules) < 1 || len(in.Rules) > 8 {
			return operationError(c, store.FieldError("rules", "provide 1-8 rules"))
		}
		if err := oc.Store.UpdateFraudRules(middleware.CurrentStaff(c).ID, in.Rules); err != nil {
			return operationError(c, err)
		}
	}
	rows, err := oc.Store.FraudRules()
	if err != nil {
		return operationError(c, err)
	}
	p, err := operationPage(c)
	if err != nil {
		return operationError(c, err)
	}
	out := []fiber.Map{}
	for _, r := range rows {
		available, reason, err := oc.Store.RuleAvailability(r.Key)
		if err != nil {
			return operationError(c, err)
		}
		out = append(out, fiber.Map{"rule": r, "effective_enabled": r.Enabled && available, "disabled_reason": reason})
	}
	start := p.Offset
	if start > len(out) {
		start = len(out)
	}
	end := start + p.Limit
	if end > len(out) {
		end = len(out)
	}
	return c.JSON(fiber.Map{"rules": out[start:end], "total": len(out), "limit": p.Limit, "offset": p.Offset})
}
func (oc *OperationsController) FraudDryRun(c *fiber.Ctx) error {
	n, err := oc.Store.DryRunFraud(c.Params("key"))
	if err != nil {
		return operationError(c, err)
	}
	return c.JSON(fiber.Map{"matching_accounts": n, "window_days": 30})
}
func (oc *OperationsController) UserFraud(c *fiber.Ctx) error {
	p, err := operationPage(c)
	if err != nil {
		return operationError(c, err)
	}
	rows := []models.FraudFlag{}
	total, err := oc.Store.PageRows(oc.Store.DB().Model(&models.FraudFlag{}).Where("user_id = ?", c.Params("id")), p, &rows)
	if err != nil {
		return operationError(c, err)
	}
	ids := map[string]bool{}
	for _, flag := range rows {
		related := []string{}
		if err := json.Unmarshal([]byte(flag.RelatedUserIDs), &related); err != nil {
			return operationError(c, err)
		}
		for _, id := range related {
			ids[id] = true
		}
	}
	devices := []models.UserDevice{}
	if err := oc.Store.DB().Where("user_id = ?", c.Params("id")).Find(&devices).Error; err != nil {
		return operationError(c, err)
	}
	hashes := []string{}
	for _, d := range devices {
		hashes = append(hashes, d.DeviceHash)
	}
	linkedDevices := []models.UserDevice{}
	if len(hashes) > 0 {
		if err := oc.Store.DB().Where("device_hash IN ? AND user_id <> ?", hashes, c.Params("id")).Order("created_at ASC,id ASC").Find(&linkedDevices).Error; err != nil {
			return operationError(c, err)
		}
	}
	for _, d := range linkedDevices {
		ids[d.UserID] = true
	}
	var u models.User
	if err := oc.Store.DB().First(&u, "id = ?", c.Params("id")).Error; err != nil {
		return operationError(c, err)
	}
	matching, err := oc.Store.LinkedIdentifiers(u.ID)
	if err != nil {
		return operationError(c, err)
	}
	for _, id := range matching {
		ids[id] = true
	}
	keys := []string{}
	for id := range ids {
		keys = append(keys, id)
	}
	sort.Strings(keys)
	linkedTotal := len(keys)
	start := p.Offset
	if start > len(keys) {
		start = len(keys)
	}
	end := start + p.Limit
	if end > len(keys) {
		end = len(keys)
	}
	linked := []store.PartySummary{}
	for _, id := range keys[start:end] {
		party, err := oc.Store.Party(id)
		if err != nil {
			return operationError(c, err)
		}
		linked = append(linked, *party)
	}
	return c.JSON(fiber.Map{"flags": rows, "linked_accounts": linked, "linked_total": linkedTotal, "total": total, "limit": p.Limit, "offset": p.Offset})
}

func (oc *OperationsController) FraudStats(c *fiber.Ctx) error {
	var count, high int64
	if err := oc.Store.DB().Model(&models.FraudFlag{}).Where("status IN ?", []string{"open", "reviewing"}).Count(&count).Error; err != nil {
		return operationError(c, err)
	}
	if err := oc.Store.DB().Model(&models.FraudFlag{}).Where("status IN ? AND severity = ?", []string{"open", "reviewing"}, "high").Count(&high).Error; err != nil {
		return operationError(c, err)
	}
	return c.JSON(fiber.Map{"open": count, "high_severity": high})
}

func (oc *OperationsController) FraudRideTrail(c *fiber.Ctx) error {
	p, err := operationPage(c)
	if err != nil {
		return operationError(c, err)
	}
	var ride models.Ride
	if err := oc.Store.DB().First(&ride, "id = ?", c.Params("id")).Error; err != nil {
		return operationError(c, err)
	}
	points := []models.RideLocationPoint{}
	total, err := oc.Store.PageRows(oc.Store.DB().Model(&models.RideLocationPoint{}).Where("ride_id = ?", ride.ID), p, &points)
	if err != nil {
		return operationError(c, err)
	}
	if err := oc.Store.CreateAuditLog(middleware.CurrentStaff(c).ID, "view_fraud_location", "ride", ride.ID, c.IP(), nil); err != nil {
		return operationError(c, err)
	}
	return c.JSON(fiber.Map{"ride": ride, "points": points, "total": total, "limit": p.Limit, "offset": p.Offset})
}
