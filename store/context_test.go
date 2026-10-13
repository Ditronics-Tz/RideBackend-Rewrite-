package store_test

import (
	"context"
	"errors"
	"testing"

	"ride-backend/models"
	"ride-backend/testutil"
)

func TestWorkerContextCancellationDoesNotCancelRequests(t *testing.T) {
	s, _, err := testutil.SetupTestStore()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	worker := s.WithContext(ctx)
	cancel()
	var total int64
	if err := worker.DB().Model(&models.User{}).Count(&total).Error; !errors.Is(err, context.Canceled) {
		t.Fatalf("worker did not cancel: %v", err)
	}
	if err := s.DB().Model(&models.User{}).Count(&total).Error; err != nil {
		t.Fatalf("request store was cancelled: %v", err)
	}
}
