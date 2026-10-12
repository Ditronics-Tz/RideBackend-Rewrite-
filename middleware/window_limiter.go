package middleware

import (
	"github.com/gofiber/fiber/v2"
	"strconv"
	"sync"
	"time"
)

// WindowLimit is per-process. Deployments with multiple API instances must use
// shared rate-limit storage; persistent financial/report guards remain in SQL.
func WindowLimit(max int, window time.Duration, key func(*fiber.Ctx) string) fiber.Handler {
	var mu sync.Mutex
	type bucket struct {
		n   int
		end time.Time
	}
	buckets := map[string]bucket{}
	lastSweep := time.Now()
	return func(c *fiber.Ctx) error {
		now := time.Now()
		mu.Lock()
		if now.Sub(lastSweep) > window {
			for k, b := range buckets {
				if !now.Before(b.end) {
					delete(buckets, k)
				}
			}
			lastSweep = now
		}
		k := key(c)
		b := buckets[k]
		if !now.Before(b.end) {
			b = bucket{end: now.Add(window)}
		}
		if b.n >= max {
			retry := int(time.Until(b.end).Seconds()) + 1
			mu.Unlock()
			c.Set("Retry-After", strconv.Itoa(retry))
			return c.Status(429).JSON(fiber.Map{"error": "request limit reached"})
		}
		b.n++
		buckets[k] = b
		mu.Unlock()
		return c.Next()
	}
}
