package notifications

import (
	"context"
	"fmt"
	"log"
	"strings"
)

type Message struct{ ID, Recipient, Phone, Template, Language, Link, Channel string }

// Providers must deduplicate by ID and use SMS fallback for the critical channel.
type Sender interface {
	Send(context.Context, Message) error
}
type Logging struct{}

func (Logging) Send(_ context.Context, m Message) error {
	if _, err := Render(m); err != nil {
		return err
	}
	log.Printf("notification accepted by mock sender: template=%s channel=%s", m.Template, m.Channel)
	return nil
}

var templates = map[string][2]string{
	"report_warning":    {"A safety concern was reviewed. Please follow Rida's community rules.", "Hoja ya usalama imekaguliwa. Tafadhali fuata kanuni za Rida."},
	"account_flag":      {"An account needs a safety review.", "Akaunti inahitaji ukaguzi wa usalama."},
	"report_escalated":  {"A report needs administrator review.", "Lalamiko linahitaji ukaguzi wa msimamizi."},
	"sos_created":       {"An emergency alert needs immediate attention.", "Tahadhari ya dharura inahitaji msaada wa haraka."},
	"sos_contact":       {"Your contact requested emergency help. Track the trip: {link}", "Mawasiliano yako ameomba msaada wa dharura. Fuatilia safari: {link}"},
	"sos_escalated":     {"An emergency alert has passed its response deadline.", "Tahadhari ya dharura imepita muda wa kuitikia."},
	"trip_shared":       {"Your contact shared a trip: {link}", "Mawasiliano yako ameshiriki safari: {link}"},
	"payment_succeeded": {"Your payment was received.", "Malipo yako yamepokelewa."},
	"payment_failed":    {"Your payment failed. Please retry or choose another method.", "Malipo hayajakamilika. Jaribu tena au chagua njia nyingine."},
	"payout_paid":       {"Your payout was sent.", "Malipo yako yametumwa."},
	"refund_processed":  {"Your refund was processed.", "Marejesho yako yamekamilika."},
	"fraud_review":      {"An account needs a fraud review.", "Akaunti inahitaji ukaguzi wa udanganyifu."},
}

func Render(m Message) (string, error) {
	pair, ok := templates[m.Template]
	if !ok {
		return "", fmt.Errorf("unknown notification template")
	}
	i := 0
	if m.Language == "sw" {
		i = 1
	}
	return strings.ReplaceAll(pair[i], "{link}", m.Link), nil
}
