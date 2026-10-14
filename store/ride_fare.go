package store

import (
	"math"
	"time"

	"ride-backend/config"
)

// EstimatedRideDuration uses the same server assumption for quotes, immediate
// rides, and scheduled dispatch. It does not claim to be a road-routing ETA.
func EstimatedRideDuration(cfg *config.Config, distanceMeters float64) (time.Duration, error) {
	if cfg.RideEstimatedSpeedKMPH <= 0 {
		return 0, OpError(503, "Ride fare estimation is not configured")
	}
	minutes := math.Ceil((distanceMeters / 1000) / float64(cfg.RideEstimatedSpeedKMPH) * 60)
	if math.IsNaN(minutes) || math.IsInf(minutes, 0) || minutes < 0 || minutes > float64(int64(math.MaxInt64)/int64(time.Minute)) {
		return 0, FieldError("fare", "ride exceeds supported fare range")
	}
	return time.Duration(minutes) * time.Minute, nil
}

func EstimatedRideFare(cfg *config.Config, distanceMeters float64) (int64, error) {
	duration, err := EstimatedRideDuration(cfg, distanceMeters)
	if err != nil {
		return 0, err
	}
	return RideFare(cfg, distanceMeters, duration)
}

func RideFare(cfg *config.Config, distanceMeters float64, duration time.Duration) (int64, error) {
	if cfg.RideBaseFareTZS <= 0 || cfg.RidePerKmFareTZS <= 0 || cfg.RidePerMinuteFareTZS <= 0 || cfg.RideEstimatedSpeedKMPH <= 0 {
		return 0, OpError(503, "Ride fares are not configured")
	}
	kilometers := math.Ceil(distanceMeters / 1000)
	minutes := math.Ceil(duration.Minutes())
	if minutes < 1 {
		minutes = 1
	}
	if kilometers < 0 || minutes < 0 || kilometers > float64(MaxMoney) || minutes > float64(MaxMoney) {
		return 0, FieldError("fare", "ride exceeds supported fare range")
	}
	kmFare, minuteFare := int64(kilometers), int64(minutes)
	if kmFare > (MaxMoney-cfg.RideBaseFareTZS)/cfg.RidePerKmFareTZS {
		return 0, FieldError("fare", "ride exceeds supported fare range")
	}
	total := cfg.RideBaseFareTZS + kmFare*cfg.RidePerKmFareTZS
	if minuteFare > (MaxMoney-total)/cfg.RidePerMinuteFareTZS {
		return 0, FieldError("fare", "ride exceeds supported fare range")
	}
	total += minuteFare * cfg.RidePerMinuteFareTZS
	if total <= 0 || total > MaxMoney {
		return 0, FieldError("fare", "ride exceeds supported fare range")
	}
	return total, nil
}
