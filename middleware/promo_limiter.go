package middleware

import (
	"github.com/gofiber/fiber/v2"
	"sync"
	"time"
)

// PromoValidationLimiter permits ten attempts in a rolling minute per app user.
// The instance belongs to one app process; a multi-instance deployment needs shared storage.
func PromoValidationLimiter() fiber.Handler {
	var mu sync.Mutex
	attempts := map[string][]time.Time{}
	lastSweep := time.Now()
	return func(c *fiber.Ctx) error {
		id, _ := CurrentUser(c)
		now := time.Now()
		cutoff := now.Add(-time.Minute)
		mu.Lock()
		if now.Sub(lastSweep) >= time.Minute {
			for key, times := range attempts {
				if len(times) == 0 || !times[len(times)-1].After(cutoff) {
					delete(attempts, key)
				}
			}
			lastSweep = now
		}
		recent := attempts[id]
		first := 0
		for first < len(recent) && !recent[first].After(cutoff) {
			first++
		}
		recent = recent[first:]
		if len(recent) >= 10 {
			mu.Unlock()
			c.Set("Retry-After", "60")
			return c.Status(429).JSON(fiber.Map{"error": "Too many promo validations; try again in a minute"})
		}
		attempts[id] = append(recent, now)
		mu.Unlock()
		return c.Next()
	}
}
