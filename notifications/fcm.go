package notifications

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// ErrInvalidPushToken means the registration token should be removed.
var ErrInvalidPushToken = errors.New("invalid push registration token")

type PushMessage struct {
	ID, Kind, Title, Body, PromoID, RideID, SharedTripID string
}

type PushSender interface {
	SendPush(context.Context, string, PushMessage) error
}

type FCMSender struct {
	client *http.Client
	url    string
}

var firebaseProjectID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9-]{3,62}$`)

// NewFCMSender uses Google Application Default Credentials, never a mobile secret.
func NewFCMSender(ctx context.Context, projectID string) (*FCMSender, error) {
	if !firebaseProjectID.MatchString(projectID) {
		return nil, errors.New("invalid FIREBASE_PROJECT_ID")
	}
	credentials, err := google.FindDefaultCredentials(ctx, "https://www.googleapis.com/auth/firebase.messaging")
	if err != nil {
		return nil, errors.New("FCM credentials unavailable")
	}
	return &FCMSender{
		client: oauth2.NewClient(ctx, credentials.TokenSource),
		url:    "https://fcm.googleapis.com/v1/projects/" + projectID + "/messages:send",
	}, nil
}

func (s *FCMSender) SendPush(ctx context.Context, token string, m PushMessage) error {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	data := map[string]string{"notification_id": m.ID, "kind": m.Kind}
	if m.PromoID != "" {
		data["promo_id"] = m.PromoID
	}
	if m.RideID != "" {
		data["ride_id"] = m.RideID
	}
	if m.SharedTripID != "" {
		data["shared_trip_id"] = m.SharedTripID
	}
	priority, ttl := "NORMAL", "86400s"
	if m.Kind == "ride" || m.Kind == "ride_offer" {
		priority, ttl = "HIGH", "120s"
	}
	if m.Kind == "ride_offer" {
		ttl = "30s"
	}
	requestBody, err := json.Marshal(map[string]any{"message": map[string]any{
		"token":        token,
		"notification": map[string]string{"title": m.Title, "body": m.Body},
		"data":         data,
		// The registration token selects the Android app within this Firebase
		// project. A passenger-only package restriction rejects driver tokens.
		"android": map[string]any{"priority": priority, "ttl": ttl, "notification": map[string]string{"channel_id": "rida_updates", "tag": m.ID}},
	}})
	if err != nil {
		return errors.New("FCM request encoding failed")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.url, bytes.NewReader(requestBody))
	if err != nil {
		return errors.New("FCM request failed")
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	response, err := s.client.Do(req)
	if err != nil {
		return errors.New("FCM transport unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return nil
	}
	// FCM errors may contain registration tokens or other private details. Parse only
	// the machine error code and never log or return the provider response body.
	var providerError struct {
		Error struct {
			Status  string `json:"status"`
			Details []struct {
				Type string `json:"@type"`
				Code string `json:"errorCode"`
			} `json:"details"`
		} `json:"error"`
	}
	_ = json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&providerError)
	if response.StatusCode == http.StatusNotFound && providerError.Error.Status == "UNREGISTERED" {
		return ErrInvalidPushToken
	}
	for _, detail := range providerError.Error.Details {
		if detail.Type == "type.googleapis.com/google.firebase.fcm.v1.FcmError" &&
			(detail.Code == "UNREGISTERED" || detail.Code == "INVALID_ARGUMENT") {
			return ErrInvalidPushToken
		}
	}
	return fmt.Errorf("FCM send rejected with status %d", response.StatusCode)
}
