package controllers

import (
	"bufio"
	"fmt"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"os"
	"path/filepath"
	"ride-backend/middleware"
	"ride-backend/models"
	"ride-backend/store"
	"ride-backend/utils"
	"strings"
	"time"
)

func (oc *OperationsController) Contacts(c *fiber.Ctx) error {
	uid, _ := middleware.CurrentUser(c)
	switch c.Method() {
	case "POST", "PUT":
		var in models.EmergencyContact
		if err := c.BodyParser(&in); err != nil {
			return operationError(c, store.FieldError("body", "invalid contact"))
		}
		in.ID = c.Params("id")
		if err := oc.Store.SaveEmergencyContact(uid, &in); err != nil {
			return operationError(c, err)
		}
		return c.JSON(in)
	case "DELETE":
		r := oc.Store.DB().Where("id = ? AND user_id = ?", c.Params("id"), uid).Delete(&models.EmergencyContact{})
		if r.Error != nil {
			return operationError(c, r.Error)
		}
		if r.RowsAffected == 0 {
			return operationError(c, store.OpError(404, "contact not found"))
		}
		return c.SendStatus(204)
	default:
		p, err := operationPage(c)
		if err != nil {
			return operationError(c, err)
		}
		rows := []models.EmergencyContact{}
		total, err := oc.Store.PageRows(oc.Store.DB().Model(&models.EmergencyContact{}).Where("user_id = ?", uid), p, &rows)
		if err != nil {
			return operationError(c, err)
		}
		return c.JSON(fiber.Map{"contacts": rows, "total": total, "limit": p.Limit, "offset": p.Offset})
	}
}
func (oc *OperationsController) AppSOS(c *fiber.Ctx) error {
	uid, _ := middleware.CurrentUser(c)
	if c.Params("rideId") != "" {
		var in store.SOSInput
		if err := c.BodyParser(&in); err != nil {
			return operationError(c, store.FieldError("location", "valid coordinates required"))
		}
		a, err := oc.Store.TriggerSOS(uid, c.Params("rideId"), oc.Cfg.PublicBaseURL, in)
		if err != nil {
			return operationError(c, err)
		}
		return c.JSON(a)
	}
	var a models.SOSAlert
	if err := oc.Store.DB().First(&a, "id = ? AND triggered_by = ?", c.Params("id"), uid).Error; err != nil {
		return operationError(c, err)
	}
	if c.Method() == "GET" {
		return c.JSON(a)
	}
	if c.Params("action") == "safe" {
		if err := oc.Store.ReportSafe(uid, a.ID, oc.Cfg.SOSFalseAlarmThreshold); err != nil {
			return operationError(c, err)
		}
	} else if c.Params("action") == "location" {
		var in struct {
			store.SOSInput
			Speed float64 `json:"speed"`
		}
		if err := c.BodyParser(&in); err != nil {
			return operationError(c, store.FieldError("location", "invalid location"))
		}
		if err := oc.Store.SOSPing(uid, a.ID, in.Lat, in.Lng, in.Accuracy, in.Speed); err != nil {
			return operationError(c, err)
		}
	} else {
		return operationError(c, store.OpError(404, "unknown action"))
	}
	if err := oc.Store.DB().First(&a, "id = ?", a.ID).Error; err != nil {
		return operationError(c, err)
	}
	return c.JSON(a)
}
func (oc *OperationsController) ShareRide(c *fiber.Ctx) error {
	uid, _ := middleware.CurrentUser(c)
	if c.Method() == "DELETE" {
		if err := oc.Store.StopSharing(c.Params("rideId"), uid); err != nil {
			return operationError(c, err)
		}
		return c.SendStatus(204)
	}
	var in struct {
		ContactIDs []string `json:"contact_ids"`
	}
	if len(c.Body()) > 0 {
		if err := c.BodyParser(&in); err != nil {
			return operationError(c, store.FieldError("body", "invalid sharing request"))
		}
	}
	token, err := oc.Store.ShareRide(c.Params("rideId"), uid, oc.Cfg.PublicBaseURL, in.ContactIDs)
	if err != nil {
		return operationError(c, err)
	}
	return c.JSON(fiber.Map{"token": token, "url": strings.TrimRight(oc.Cfg.PublicBaseURL, "/") + "/api/v1/share/" + token, "expires_in": 43200})
}
func (oc *OperationsController) PublicShare(c *fiber.Ctx) error {
	c.Set("Cache-Control", "no-store")
	c.Set("X-Robots-Tag", "noindex")
	out, err := oc.Store.PublicShare(c.Params("token"))
	if err != nil {
		return operationError(c, err)
	}
	return c.JSON(out)
}
func PublicShareLimiter() fiber.Handler {
	return middleware.WindowLimit(60, time.Minute, func(c *fiber.Ctx) string { return c.IP() })
}

func (oc *OperationsController) SOSList(c *fiber.Ctx) error {
	p, err := operationPage(c)
	if err != nil {
		return operationError(c, err)
	}
	q := oc.Store.DB().Model(&models.SOSAlert{})
	if c.Query("status") == "open" {
		q = q.Where("resolved_at IS NULL")
	} else if c.Query("status") != "" {
		q = q.Where("status = ?", c.Query("status"))
	}
	for col, v := range map[string]string{"outcome": c.Query("outcome"), "assigned_to": c.Query("staff")} {
		if v != "" {
			q = q.Where(col+" = ?", v)
		}
	}
	for col, v := range map[string]string{"created_at >= ?": c.Query("from"), "created_at < ?": c.Query("to")} {
		if v != "" {
			ts, err := time.Parse(time.RFC3339, v)
			if err != nil {
				return operationError(c, store.FieldError("date", "date must be RFC3339"))
			}
			q = q.Where(col, ts)
		}
	}
	rows := []models.SOSAlert{}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return operationError(c, err)
	}
	if err := q.Order("escalation_level DESC, created_at ASC, id ASC").Limit(p.Limit).Offset(p.Offset).Find(&rows).Error; err != nil {
		return operationError(c, err)
	}
	return c.JSON(fiber.Map{"alerts": rows, "total": total, "limit": p.Limit, "offset": p.Offset, "sla_seconds": oc.Cfg.SOSAckSLASeconds})
}
func maskSafetyPhone(phone string) string {
	if len(phone) < 7 {
		return "***"
	}
	return phone[:5] + "** *** " + phone[len(phone)-3:]
}
func (oc *OperationsController) SOSDetail(c *fiber.Ctx) error {
	p, err := operationPage(c)
	if err != nil {
		return operationError(c, err)
	}
	var a models.SOSAlert
	if err := oc.Store.DB().First(&a, "id = ?", c.Params("id")).Error; err != nil {
		return operationError(c, err)
	}
	var ride models.Ride
	if err := oc.Store.DB().First(&ride, "id = ?", a.RideID).Error; err != nil {
		return operationError(c, err)
	}
	driver, err := oc.Store.Party(ride.DriverID)
	if err != nil {
		return operationError(c, err)
	}
	passenger, err := oc.Store.Party(ride.PassengerID)
	if err != nil {
		return operationError(c, err)
	}
	var dp models.DriverProfile
	if err := oc.Store.DB().First(&dp, "user_id = ?", ride.DriverID).Error; err != nil {
		return operationError(c, err)
	}
	contacts := []models.EmergencyContact{}
	if err := oc.Store.DB().Where("user_id = ?", a.TriggeredBy).Order("created_at ASC,id ASC").Limit(5).Find(&contacts).Error; err != nil {
		return operationError(c, err)
	}
	reveal := c.QueryBool("reveal")
	for i := range contacts {
		if !reveal {
			contacts[i].Phone = maskSafetyPhone(contacts[i].Phone)
		}
	}
	trail := []models.SOSLocationPing{}
	trailTotal, err := oc.Store.PageRows(oc.Store.DB().Model(&models.SOSLocationPing{}).Where("alert_id = ?", a.ID), p, &trail)
	if err != nil {
		return operationError(c, err)
	}
	events := []models.SOSEvent{}
	eventsTotal, err := oc.Store.PageRows(oc.Store.DB().Model(&models.SOSEvent{}).Where("alert_id = ?", a.ID), p, &events)
	if err != nil {
		return operationError(c, err)
	}
	flags := []models.AccountFlag{}
	if err := oc.Store.DB().Where("user_id IN ? AND status = ?", []string{ride.DriverID, ride.PassengerID}, "open").Order("created_at DESC,id DESC").Limit(100).Find(&flags).Error; err != nil {
		return operationError(c, err)
	}
	phones := map[string]string{}
	for _, id := range []string{ride.DriverID, ride.PassengerID} {
		var u models.User
		if err := oc.Store.DB().First(&u, "id = ?", id).Error; err != nil {
			return operationError(c, err)
		}
		phone := maskSafetyPhone(u.Phone)
		if reveal {
			phone = u.Phone
		}
		phones[id] = phone
	}
	if err := oc.Store.CreateAuditLog(middleware.CurrentStaff(c).ID, "sos_sensitive_view", "sos", a.ID, c.IP(), fiber.Map{"contacts_revealed": reveal, "location_trail": true}); err != nil {
		return operationError(c, err)
	}
	return c.JSON(fiber.Map{"alert": a, "ride": ride, "driver": driver, "passenger": passenger, "vehicle": dp.PlateNumber, "contacts": contacts, "phones": phones, "trail": trail, "trail_total": trailTotal, "timeline": events, "timeline_total": eventsTotal, "flags": flags})
}
func (oc *OperationsController) SOSAction(c *fiber.Ctx) error {
	var in store.SOSAction
	if len(c.Body()) > 0 {
		if err := c.BodyParser(&in); err != nil {
			return operationError(c, store.FieldError("body", "invalid action"))
		}
	}
	if err := oc.Store.WorkSOS(c.Params("id"), c.Params("action"), middleware.CurrentStaff(c), in, oc.Cfg.SOSFalseAlarmThreshold); err != nil {
		return operationError(c, err)
	}
	return c.JSON(fiber.Map{"id": c.Params("id"), "action": c.Params("action")})
}
func (oc *OperationsController) SOSStream(c *fiber.Ctx) error {
	events, unsubscribe := oc.Store.Events.Subscribe()
	actor := middleware.CurrentStaff(c)
	cfg := oc.Cfg
	s := oc.Store
	id := actor.ID
	role := actor.Role
	streamTTL := cfg.StaffAccessTTL
	if expires, ok := c.Locals("staff_expires_at").(time.Time); ok {
		streamTTL = time.Until(expires)
	}
	c.Set("Content-Type", "text/event-stream")
	c.Set("Cache-Control", "no-store")
	c.Set("Connection", "keep-alive")
	c.Set("X-Accel-Buffering", "no")
	c.Context().SetBodyStreamWriter(func(w *bufio.Writer) {
		defer unsubscribe()
		heartbeat := time.NewTicker(15 * time.Second)
		defer heartbeat.Stop()
		deadline := time.NewTimer(streamTTL)
		defer deadline.Stop()
		if _, err := fmt.Fprint(w, ": connected\n\n"); err != nil {
			return
		}
		if err := w.Flush(); err != nil {
			return
		}
		for {
			select {
			case <-deadline.C:
				return
			case event := <-events:
				if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Type, event.Data); err != nil {
					return
				}
			case <-heartbeat.C:
				var current models.StaffUser
				err := s.DB().First(&current, "id = ?", id).Error
				if err != nil || !current.IsActive || current.Role != role {
					return
				}
				if _, err := fmt.Fprint(w, ": heartbeat\n\n"); err != nil {
					return
				}
			}
			if err := w.Flush(); err != nil {
				return
			}
		}
	})
	return nil
}
func (oc *OperationsController) Incidents(c *fiber.Ctx) error {
	uid, _ := middleware.CurrentUser(c)
	staff := middleware.CurrentStaff(c)
	if c.Method() == "POST" {
		var in models.IncidentReport
		if err := c.BodyParser(&in); err != nil {
			return operationError(c, store.FieldError("body", "invalid incident"))
		}
		if err := oc.Store.CreateIncident(uid, &in); err != nil {
			return operationError(c, err)
		}
		return c.Status(201).JSON(in)
	}
	p, err := operationPage(c)
	if err != nil {
		return operationError(c, err)
	}
	q := oc.Store.DB().Model(&models.IncidentReport{})
	if staff == nil {
		q = q.Where("reporter_id = ?", uid)
	}
	if c.Query("status") != "" {
		q = q.Where("status = ?", c.Query("status"))
	}
	rows := []models.IncidentReport{}
	total, err := oc.Store.PageRows(q, p, &rows)
	if err != nil {
		return operationError(c, err)
	}
	return c.JSON(fiber.Map{"incidents": rows, "total": total, "limit": p.Limit, "offset": p.Offset})
}
func (oc *OperationsController) IncidentDetail(c *fiber.Ctx) error {
	p, err := operationPage(c)
	if err != nil {
		return operationError(c, err)
	}
	var r models.IncidentReport
	if err := oc.Store.DB().First(&r, "id = ?", c.Params("id")).Error; err != nil {
		return operationError(c, err)
	}
	notes := []models.IncidentNote{}
	total, err := oc.Store.PageRows(oc.Store.DB().Model(&models.IncidentNote{}).Where("incident_id = ?", r.ID), p, &notes)
	if err != nil {
		return operationError(c, err)
	}
	files := []models.IncidentAttachment{}
	if err := oc.Store.DB().Where("incident_id = ?", r.ID).Order("created_at ASC,id ASC").Limit(20).Find(&files).Error; err != nil {
		return operationError(c, err)
	}
	return c.JSON(fiber.Map{"incident": r, "notes": notes, "notes_total": total, "attachments": files})
}
func (oc *OperationsController) IncidentAction(c *fiber.Ctx) error {
	var in struct {
		Note string `json:"note"`
	}
	if len(c.Body()) > 0 {
		if err := c.BodyParser(&in); err != nil {
			return operationError(c, store.FieldError("note", "invalid note"))
		}
	}
	if err := oc.Store.WorkIncident(c.Params("id"), c.Params("action"), middleware.CurrentStaff(c), in.Note); err != nil {
		return operationError(c, err)
	}
	return c.JSON(fiber.Map{"id": c.Params("id")})
}
func (oc *OperationsController) IncidentAttachment(c *fiber.Ctx) error {
	uid, _ := middleware.CurrentUser(c)
	if c.Method() == "GET" {
		var f models.IncidentAttachment
		q := oc.Store.DB().Where("id = ?", c.Params("id"))
		if middleware.CurrentStaff(c) == nil {
			q = q.Where("user_id = ?", uid)
		}
		if err := q.First(&f).Error; err != nil {
			return operationError(c, err)
		}
		c.Set("Cache-Control", "no-store")
		return c.SendFile(f.FilePath)
	}
	var r models.IncidentReport
	if err := oc.Store.DB().First(&r, "id = ? AND reporter_id = ?", c.Params("id"), uid).Error; err != nil {
		return operationError(c, err)
	}
	var n int64
	if err := oc.Store.DB().Model(&models.IncidentAttachment{}).Where("incident_id = ?", r.ID).Count(&n).Error; err != nil {
		return operationError(c, err)
	}
	if n >= 10 {
		return operationError(c, store.OpError(409, "maximum 10 attachments"))
	}
	url, err := utils.SaveLicenceImage(c, "file", "incident")
	if err != nil {
		return operationError(c, store.FieldError("file", err.Error()))
	}
	if url == "" {
		return operationError(c, store.FieldError("file", "file is required"))
	}
	f := models.IncidentAttachment{ID: uuid.NewString(), IncidentID: r.ID, UserID: uid, FilePath: filepath.Join("uploads", "licences", filepath.Base(url)), CreatedAt: time.Now().UTC()}
	if err := oc.Store.DB().Create(&f).Error; err != nil {
		if cleanupErr := os.Remove(f.FilePath); cleanupErr != nil {
			return operationError(c, cleanupErr)
		}
		return operationError(c, err)
	}
	return c.Status(201).JSON(f)
}
