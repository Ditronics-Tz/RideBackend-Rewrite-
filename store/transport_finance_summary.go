package store

import "time"

type TransportFinanceTotal struct {
	GMV        int64 `json:"gmv"`
	Commission int64 `json:"commission"`
}
type TransportFinanceDay struct {
	Date       string `json:"date"`
	GMV        int64  `json:"gmv"`
	Commission int64  `json:"commission"`
}

// Include assessments before collection, legacy settled hails, and pre-upgrade
// shared completions. Never count an assessed hail again when cash is recorded.
func (s *Store) TransportFinanceSummary() (TransportFinanceTotal, []TransportFinanceDay, int64, error) {
	var total TransportFinanceTotal
	days := []TransportFinanceDay{}
	stamp := "to_timestamp(completed)"
	day := "to_char(at AT TIME ZONE 'Africa/Dar_es_Salaam','YYYY-MM-DD')"
	assessedStamp := "to_timestamp(created)"
	if s.db.Dialector.Name() != "postgres" {
		stamp = "datetime(completed,'unixepoch')"
		assessedStamp = "datetime(created,'unixepoch')"
		day = "date(at,'+3 hours')"
	}
	union := `SELECT fare AS gross,commission AS fee,created_at AS at FROM payment_transactions p WHERE ride_id IS NOT NULL AND status IN ('succeeded','partially_refunded','refunded') AND NOT EXISTS (SELECT 1 FROM transport_assessments a WHERE a.mode='hail' AND a.trip_id=p.ride_id)
 UNION ALL SELECT gross,fee,` + assessedStamp + ` AS at FROM transport_assessments
 UNION ALL SELECT price*(SELECT count(*) FROM shared_seats b WHERE b.trip_id=t.id AND b.status IN ('confirmed','completed')) AS gross,fee,` + stamp + ` AS at FROM shared_trips t WHERE status='completed' AND NOT EXISTS (SELECT 1 FROM transport_assessments a WHERE a.mode='shared' AND a.trip_id=t.id)`
	if err := s.db.Raw("SELECT coalesce(sum(gross),0) AS gmv,coalesce(sum(fee),0) AS commission FROM (" + union + ") AS transport").Scan(&total).Error; err != nil {
		return total, days, 0, err
	}
	if err := s.db.Raw("SELECT "+day+" AS date,sum(gross) AS gmv,sum(fee) AS commission FROM ("+union+") AS transport WHERE at>=? GROUP BY "+day+" ORDER BY date", s.now().Add(-30*24*time.Hour)).Scan(&days).Error; err != nil {
		return total, days, 0, err
	}
	var outstanding int64
	err := s.db.Table("transport_assessments").Where("status='receivable'").Select("coalesce(sum(fee),0)").Scan(&outstanding).Error
	return total, days, outstanding, err
}
