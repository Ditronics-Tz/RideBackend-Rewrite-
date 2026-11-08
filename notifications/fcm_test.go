package notifications

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFCMAllowsBothAppsAndPreservesSharedTripReference(t *testing.T) {
	for _, token := range []string{"passenger-fixture-token", "driver-fixture-token"} {
		t.Run(token, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var payload struct {
					Message struct {
						Token   string            `json:"token"`
						Data    map[string]string `json:"data"`
						Android map[string]any    `json:"android"`
					} `json:"message"`
				}
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				if payload.Message.Token != token || payload.Message.Data["shared_trip_id"] != "trip-fixture" {
					t.Error("push target or shared trip reference lost")
				}
				if _, restricted := payload.Message.Android["restricted_package_name"]; restricted {
					t.Error("package restriction prevents delivery to both Android apps")
				}
				if payload.Message.Android["priority"] != "HIGH" {
					t.Error("ride updates must use high priority")
				}
				w.WriteHeader(http.StatusOK)
			}))
			defer server.Close()
			sender := &FCMSender{client: server.Client(), url: server.URL}
			if err := sender.SendPush(context.Background(), token, PushMessage{ID: "notification-fixture", Kind: "ride", Title: "Ride update", Body: "Open Rida", SharedTripID: "trip-fixture"}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
