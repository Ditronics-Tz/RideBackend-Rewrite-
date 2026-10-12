package controllers

import (
	"bufio"
	"encoding/csv"
	"github.com/gofiber/fiber/v2"
	"log"
	"ride-backend/middleware"
	"ride-backend/models"
	"ride-backend/store"
	"time"
)

func (oc *OperationsController) SOSCSV(c *fiber.Ctx) error {
	q := oc.Store.DB().Model(&models.SOSAlert{})
	for col, value := range map[string]string{"outcome": c.Query("outcome"), "assigned_to": c.Query("staff")} {
		if value != "" {
			q = q.Where(col+" = ?", value)
		}
	}
	for col, value := range map[string]string{"created_at >= ?": c.Query("from"), "created_at < ?": c.Query("to")} {
		if value != "" {
			ts, err := time.Parse(time.RFC3339, value)
			if err != nil {
				return operationError(c, store.FieldError("date", "date must be RFC3339"))
			}
			q = q.Where(col, ts)
		}
	}
	if err := oc.Store.CreateAuditLog(middleware.CurrentStaff(c).ID, "export_sos_history", "sos", "collection", c.IP(), nil); err != nil {
		return operationError(c, err)
	}
	c.Set("Content-Type", "text/csv")
	c.Set("Content-Disposition", "attachment; filename=sos-history.csv")
	c.Set("Cache-Control", "no-store")
	c.Context().SetBodyStreamWriter(func(w *bufio.Writer) {
		writer := csv.NewWriter(w)
		if err := writer.Write([]string{"id", "ride_id", "status", "outcome", "assigned_to", "created_at", "resolved_at"}); err != nil {
			return
		}
		last := ""
		for {
			rows := []models.SOSAlert{}
			page := q.Order("id ASC").Limit(100)
			if last != "" {
				page = page.Where("id > ?", last)
			}
			if err := page.Find(&rows).Error; err != nil {
				log.Print("SOS CSV database read failed; stream terminated")
				return
			}
			for _, a := range rows {
				staff, resolved := "", ""
				if a.AssignedTo != nil {
					staff = *a.AssignedTo
				}
				if a.ResolvedAt != nil {
					resolved = a.ResolvedAt.Format(time.RFC3339)
				}
				if err := writer.Write([]string{a.ID, a.RideID, a.Status, a.Outcome, staff, a.CreatedAt.Format(time.RFC3339), resolved}); err != nil {
					return
				}
				last = a.ID
			}
			writer.Flush()
			if writer.Error() != nil {
				return
			}
			if len(rows) < 100 {
				return
			}
		}
	})
	return nil
}
