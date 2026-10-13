package store

import (
	"context"
	"encoding/json"
	"gorm.io/gorm/clause"
	"math"
	"ride-backend/models"
	"sort"
	"time"
)

type Finding struct {
	UserID         string
	RuleKey        string
	Evidence       map[string]any
	RelatedUserIDs []string
	Score          int
}
type Rule interface {
	Key() string
	Evaluate(context.Context, models.FraudEvent) ([]Finding, error)
}
type builtinRule struct {
	s        *Store
	settings models.FraudRule
}

func (r builtinRule) Key() string { return r.settings.Key }

var fraudDefaults = map[string]string{
	"shared_device":         `{"max_accounts":3,"window_days":30}`,
	"duplicate_identifiers": `{"window_days":30}`,
	"cancellation_abuse":    `{"min_rides":10,"cancel_percent":60,"window_days":7}`,
	"location_mismatch":     `{"min_points":3,"min_expected_m":1000,"min_distance_percent":20,"max_speed_mps":60,"max_route_deviation_m":1000,"max_repeated_points":10,"window_days":30}`,
	"collusion":             `{"max_pair_daily":8,"short_distance_m":500,"high_fare":20000,"min_short_rides":4,"window_days":30}`,
	"rating_manipulation":   `{"max_pair_fives":5,"min_new_account_ones":3,"new_account_days":7,"window_days":30}`,
	"promo_referral_abuse":  `{"max_device_referrals":5,"max_device_redemptions":10,"min_unqualified_referrals":5,"window_days":30}`,
	"payout_anomaly":        `{"cash_ride_burst":8,"max_destination_accounts":3,"window_days":1}`,
}

func (s *Store) SeedFraudRules() error {
	for key, params := range fraudDefaults {
		severity := "medium"
		mitigation := ""
		if key == "duplicate_identifiers" || key == "location_mismatch" || key == "payout_anomaly" {
			severity = "high"
			mitigation = "payout_hold,promo_block"
		}
		if err := s.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&models.FraudRule{Key: key, Enabled: true, Params: params, Severity: severity, AutoMitigation: mitigation, CreatedAt: s.now()}).Error; err != nil {
			return err
		}
	}
	return nil
}
func ruleParams(r models.FraudRule) (map[string]float64, error) {
	params := map[string]float64{}
	if err := json.Unmarshal([]byte(r.Params), &params); err != nil {
		return nil, FieldError("params", "parameters must be a numeric JSON object")
	}
	return params, nil
}
func (s *Store) RuleAvailability(key string) (bool, string, error) {
	model := any(nil)
	switch key {
	case "shared_device":
		if s.Policy == nil || s.Policy.DeviceHashPepper == "" {
			return false, "DEVICE_HASH_PEPPER is not configured", nil
		}
		model = &models.UserDevice{}
	case "location_mismatch":
		model = &models.RideLocationPoint{}
	case "cancellation_abuse", "collusion":
		model = &models.Ride{}
	case "rating_manipulation":
		model = &models.Rating{}
	case "promo_referral_abuse":
		model = &models.UserDevice{}
	case "payout_anomaly":
		model = &models.DriverPayout{}
	case "duplicate_identifiers":
		return true, "", nil
	default:
		return false, "no registered evaluator", nil
	}
	var n int64
	if err := s.db.Model(model).Limit(1).Count(&n).Error; err != nil {
		return false, "", err
	}
	if n == 0 {
		return false, "required event data is not available yet", nil
	}
	return true, "", nil
}
func (r builtinRule) Evaluate(ctx context.Context, event models.FraudEvent) ([]Finding, error) {
	p, err := ruleParams(r.settings)
	if err != nil {
		return nil, err
	}
	s := r.s
	db := s.db.WithContext(ctx)
	cutoff := s.now().AddDate(0, 0, -int(p["window_days"]))
	ids := []string{}
	evidence := map[string]any{}
	found := false
	switch r.Key() {
	case "shared_device", "promo_referral_abuse":
		devices := []models.UserDevice{}
		if err := db.Where("user_id = ? AND last_seen >= ?", event.UserID, cutoff).Find(&devices).Error; err != nil {
			return nil, err
		}
		hashes := []string{}
		ips := []string{}
		for _, d := range devices {
			hashes = append(hashes, d.DeviceHash)
			if d.LastIP != "" {
				ips = append(ips, d.LastIP)
			}
		}
		if len(hashes) == 0 {
			return nil, nil
		}
		others := []models.UserDevice{}
		q := db.Where("last_seen >= ? AND device_hash IN ?", cutoff, hashes)
		if r.Key() == "promo_referral_abuse" && len(ips) > 0 {
			q = db.Where("last_seen >= ? AND (device_hash IN ? OR last_ip IN ?)", cutoff, hashes, ips)
		}
		if err := q.Find(&others).Error; err != nil {
			return nil, err
		}
		seen := map[string]bool{}
		for _, d := range others {
			seen[d.UserID] = true
		}
		for id := range seen {
			ids = append(ids, id)
		}
		if r.Key() == "shared_device" {
			accountsByDevice := map[string]map[string]bool{}
			for _, d := range others {
				if accountsByDevice[d.DeviceHash] == nil {
					accountsByDevice[d.DeviceHash] = map[string]bool{}
				}
				accountsByDevice[d.DeviceHash][d.UserID] = true
			}
			linked := map[string]bool{}
			maxAccounts := 0
			for _, accounts := range accountsByDevice {
				if len(accounts) > int(p["max_accounts"]) {
					found = true
					if len(accounts) > maxAccounts {
						maxAccounts = len(accounts)
					}
					for id := range accounts {
						linked[id] = true
					}
				}
			}
			ids = []string{}
			for id := range linked {
				ids = append(ids, id)
			}
			evidence["account_count"] = maxAccounts
		} else {
			var referrals, redemptions, unqualified int64
			if err := db.Model(&models.Referral{}).Where("referee_id IN ? AND created_at >= ?", ids, cutoff).Count(&referrals).Error; err != nil {
				return nil, err
			}
			if err := db.Model(&models.PromoRedemption{}).Where("user_id IN ? AND created_at >= ? AND status IN ?", ids, cutoff, []string{"reserved", "applied"}).Count(&redemptions).Error; err != nil {
				return nil, err
			}
			if err := db.Model(&models.Referral{}).Where("referrer_id = ? AND status = ? AND created_at >= ?", event.UserID, "pending", cutoff).Count(&unqualified).Error; err != nil {
				return nil, err
			}
			found = referrals > int64(p["max_device_referrals"]) || redemptions > int64(p["max_device_redemptions"]) || unqualified >= int64(p["min_unqualified_referrals"])
			evidence = map[string]any{"referrals": referrals, "redemptions": redemptions, "unqualified_referrals": unqualified}
		}
	case "duplicate_identifiers":
		var u models.User
		if err := db.First(&u, "id = ?", event.UserID).Error; err != nil {
			return nil, err
		}
		var profile models.DriverProfile
		err := db.First(&profile, "user_id = ?", event.UserID).Error
		if err != nil && !notFound(err) {
			return nil, err
		}
		users := []models.User{}
		if err := db.Where("id <> ? AND status <> ?", event.UserID, "deleted").Find(&users).Error; err != nil {
			return nil, err
		}
		for _, other := range users {
			if u.Phone != "" && normalizeIdentity(u.Phone) == normalizeIdentity(other.Phone) {
				ids = append(ids, other.ID)
			}
		}
		drivers := []models.DriverProfile{}
		if err := db.Where("user_id <> ?", event.UserID).Find(&drivers).Error; err != nil {
			return nil, err
		}
		for _, other := range drivers {
			if (profile.PlateNumber != "" && normalizeIdentity(profile.PlateNumber) == normalizeIdentity(other.PlateNumber)) || (profile.LicenceNumber != "" && normalizeIdentity(profile.LicenceNumber) == normalizeIdentity(other.LicenceNumber)) {
				ids = append(ids, other.UserID)
			}
		}
		found = len(ids) > 0
		evidence["matching_accounts"] = len(ids)
	case "cancellation_abuse":
		rides := []models.Ride{}
		if err := db.Where("(passenger_id = ? OR driver_id = ?) AND created_at >= ?", event.UserID, event.UserID, cutoff).Find(&rides).Error; err != nil {
			return nil, err
		}
		cancelled := 0
		eligible := 0
		for _, ride := range rides {
			if ride.DriverID == event.UserID && ride.AcceptedAt == nil {
				continue
			}
			eligible++
			if ride.Status == "cancelled" && ride.CancelledBy != nil && *ride.CancelledBy == event.UserID {
				cancelled++
			}
		}
		found = eligible >= int(p["min_rides"]) && cancelled*100 > eligible*int(p["cancel_percent"])
		evidence = map[string]any{"rides": eligible, "cancelled": cancelled}
	case "location_mismatch":
		rides := []models.Ride{}
		if err := db.Where("driver_id = ? AND status = ? AND created_at >= ? AND expected_distance >= ?", event.UserID, "completed", cutoff, p["min_expected_m"]).Find(&rides).Error; err != nil {
			return nil, err
		}
		badRides := []string{}
		for _, ride := range rides {
			points := []models.RideLocationPoint{}
			if err := db.Where("ride_id = ? AND accuracy <= ?", ride.ID, 100).Order("at ASC,id ASC").Find(&points).Error; err != nil {
				return nil, err
			}
			if len(points) < int(p["min_points"]) {
				continue
			}
			route := [][2]float64{}
			if ride.RouteGeometry != "" {
				if err := json.Unmarshal([]byte(ride.RouteGeometry), &route); err != nil {
					return nil, err
				}
			}
			offRoute := 0
			if len(route) > 1 {
				for _, point := range points {
					if DistanceFromRoute(point.Lat, point.Lng, route) > p["max_route_deviation_m"] {
						offRoute++
					}
				}
			}
			distance := 0.0
			impossible := false
			repeats := 0
			for i := 1; i < len(points); i++ {
				d := Haversine(points[i-1].Lat, points[i-1].Lng, points[i].Lat, points[i].Lng)
				distance += d
				seconds := points[i].At.Sub(points[i-1].At).Seconds()
				if seconds > 0 && d/seconds > p["max_speed_mps"] {
					impossible = true
				}
				if d < 1 {
					repeats++
				}
			}
			if distance < ride.ExpectedDistance*p["min_distance_percent"]/100 || impossible || (len(route) > 1 && offRoute*2 > len(points)) || repeats >= int(p["max_repeated_points"]) {
				badRides = append(badRides, ride.ID)
			}
		}
		found = len(badRides) > 0
		evidence["ride_ids"] = badRides
		evidence["ride_count"] = len(badRides)
	case "collusion":
		rides := []models.Ride{}
		if err := db.Where("(driver_id = ? OR passenger_id = ?) AND status = ? AND created_at >= ?", event.UserID, event.UserID, "completed", cutoff).Find(&rides).Error; err != nil {
			return nil, err
		}
		daily := map[string]int{}
		short := map[string]int{}
		for _, ride := range rides {
			pair := ride.DriverID + ":" + ride.PassengerID + ":" + ride.CreatedAt.UTC().Format("2006-01-02")
			daily[pair]++
			other := ride.DriverID
			if other == event.UserID {
				other = ride.PassengerID
			}
			if ride.ExpectedDistance > 0 && ride.ExpectedDistance < p["short_distance_m"] && ride.Fare >= int64(p["high_fare"]) {
				short[other]++
			}
			if daily[pair] > int(p["max_pair_daily"]) || short[other] >= int(p["min_short_rides"]) {
				found = true
				ids = append(ids, other)
			}
		}
		evidence["matching_pairs"] = len(ids)
	case "rating_manipulation":
		ratings := []models.Rating{}
		if err := db.Where("(from_user_id = ? OR to_user_id = ?) AND created_at >= ?", event.UserID, event.UserID, cutoff).Find(&ratings).Error; err != nil {
			return nil, err
		}
		fives := map[string]int{}
		incoming := map[string]int{}
		outgoing := map[string]int{}
		ones := map[string]bool{}
		for _, rating := range ratings {
			other := rating.FromUserID
			if other == event.UserID {
				other = rating.ToUserID
			}
			if rating.Score == 5 {
				fives[other]++
				if rating.FromUserID == event.UserID {
					outgoing[other]++
				} else {
					incoming[other]++
				}
			}
			if rating.Score == 1 && rating.ToUserID == event.UserID {
				var author models.User
				if err := db.First(&author, "id = ?", rating.FromUserID).Error; err != nil {
					return nil, err
				}
				if rating.CreatedAt.Sub(author.CreatedAt) < time.Duration(p["new_account_days"])*24*time.Hour {
					ones[author.ID] = true
				}
			}
		}
		for id, n := range fives {
			if n > int(p["max_pair_fives"]) && incoming[id] > 0 && outgoing[id] > 0 {
				ids = append(ids, id)
				found = true
			}
		}
		if len(ones) >= int(p["min_new_account_ones"]) {
			found = true
			for id := range ones {
				ids = append(ids, id)
			}
		}
		evidence["new_account_low_ratings"] = len(ones)
		evidence["linked_accounts"] = len(ids)
	case "payout_anomaly":
		payouts := []models.DriverPayout{}
		if err := db.Where("driver_id = ? AND created_at >= ?", event.UserID, cutoff).Find(&payouts).Error; err != nil {
			return nil, err
		}
		for _, payout := range payouts {
			var cash int64
			if err := db.Model(&models.PaymentTransaction{}).Where("driver_id = ? AND method = ? AND status = ? AND created_at <= ? AND created_at >= ?", event.UserID, "cash", "succeeded", payout.CreatedAt, payout.CreatedAt.Add(-time.Hour)).Count(&cash).Error; err != nil {
				return nil, err
			}
			if cash >= int64(p["cash_ride_burst"]) {
				found = true
				evidence["cash_ride_count"] = cash
			}
			if payout.DestinationHash != "" {
				var n int64
				if err := db.Model(&models.DriverPayout{}).Where("destination_hash = ? AND created_at >= ?", payout.DestinationHash, cutoff).Distinct("driver_id").Count(&n).Error; err != nil {
					return nil, err
				}
				if n > int64(p["max_destination_accounts"]) {
					found = true
					evidence["destination_account_count"] = n
				}
			}
		}
	}
	if !found {
		return nil, nil
	}
	unique := map[string]bool{}
	outIDs := []string{}
	for _, id := range ids {
		if id != event.UserID && !unique[id] {
			unique[id] = true
			outIDs = append(outIDs, id)
		}
	}
	sort.Strings(outIDs)
	return []Finding{{UserID: event.UserID, RuleKey: r.Key(), Evidence: evidence, RelatedUserIDs: outIDs, Score: 50}}, nil
}
func Haversine(a, b, c, d float64) float64 {
	const earth = 6371000
	lat1, lat2 := a*math.Pi/180, c*math.Pi/180
	dl, do := (c-a)*math.Pi/180, (d-b)*math.Pi/180
	x := math.Sin(dl/2)*math.Sin(dl/2) + math.Cos(lat1)*math.Cos(lat2)*math.Sin(do/2)*math.Sin(do/2)
	return earth * 2 * math.Atan2(math.Sqrt(x), math.Sqrt(math.Max(0, 1-x)))
}

// DistanceFromRoute projects short route segments locally; Haversine determines the final distance.
func DistanceFromRoute(lat, lng float64, route [][2]float64) float64 {
	best := math.Inf(1)
	scale := math.Cos(lat * math.Pi / 180)
	for i := 1; i < len(route); i++ {
		a, b := route[i-1], route[i]
		dx, dy := (b[1]-a[1])*scale, b[0]-a[0]
		t := 0.0
		if dx*dx+dy*dy > 0 {
			t = ((lng-a[1])*scale*dx + (lat-a[0])*dy) / (dx*dx + dy*dy)
		}
		t = math.Max(0, math.Min(1, t))
		d := Haversine(lat, lng, a[0]+t*(b[0]-a[0]), a[1]+t*(b[1]-a[1]))
		best = math.Min(best, d)
	}
	return best
}
