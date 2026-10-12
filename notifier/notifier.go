package notifier

import (
	"context"
	"log"
)

type PromoMessage struct{ PromoID, Code, Title, Description string }
type Notifier interface {
	NotifyPromo(context.Context, string, PromoMessage) error
}

// Logging accepts notifications without sending SMS/push or logging recipients.
// Replace it before enabling real delivery; successful calls are tracked by share.
type Logging struct{}

func (Logging) NotifyPromo(_ context.Context, _ string, _ PromoMessage) error {
	log.Print("Promo notification accepted by no-op notifier; no SMS/push sent")
	return nil
}
