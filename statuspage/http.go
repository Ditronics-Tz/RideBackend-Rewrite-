package statuspage

import (
	"context"

	"github.com/gofiber/fiber/v2"
)

// Options configures the monitor attached to this process.
type Options struct {
	Dir            string
	Version        string
	Ping           func(context.Context) error
	PushEnabled    bool
	BackgroundJobs bool
}

// Attach registers the public status page and JSON snapshot, takes one sample,
// and keeps the monitor for the background sampler started from main.
func Attach(app *fiber.App, api fiber.Router, opts Options) *Monitor {
	monitor := New(opts.Dir, nil)
	if opts.Version != "" {
		monitor.setVersion(opts.Version)
	}
	for _, probe := range DefaultProbes(Deps{
		Ping: opts.Ping, PushEnabled: opts.PushEnabled, BackgroundJobs: opts.BackgroundJobs,
		Now: monitor.clock, Started: monitor.startedAt,
	}) {
		monitor.Add(probe)
	}
	monitor.Tick(context.Background())
	active.Store(monitor)
	app.Get("/status", monitor.ServePage)
	api.Get("/status", monitor.ServeJSON)
	return monitor
}

// ServePage renders the public HTML status page.
func (m *Monitor) ServePage(c *fiber.Ctx) error {
	setStatusHeaders(c)
	body, err := renderPage(m.PublicSnapshot())
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).SendString("Status page is unavailable")
	}
	c.Type("html", "utf-8")
	return c.Send(body)
}

// ServeJSON returns the anonymous status snapshot.
func (m *Monitor) ServeJSON(c *fiber.Ctx) error {
	setStatusHeaders(c)
	return c.JSON(m.PublicSnapshot())
}

// ServeStaff returns probe detail for an authenticated admin request.
func (m *Monitor) ServeStaff(c *fiber.Ctx) error {
	c.Set("Cache-Control", "no-store")
	c.Set("X-Content-Type-Options", "nosniff")
	return c.JSON(m.Snapshot())
}

func setStatusHeaders(c *fiber.Ctx) {
	c.Set("Cache-Control", "no-store")
	c.Set("X-Content-Type-Options", "nosniff")
	c.Set("Referrer-Policy", "no-referrer")
	c.Set("X-Frame-Options", "DENY")
	c.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; img-src 'none'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
}
