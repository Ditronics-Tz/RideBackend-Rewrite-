package store

import (
	"errors"
	"time"

	"ride-backend/models"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Store is a Postgres-backed store (GORM).
// Controllers only depend on these methods, so the DB engine
// can be swapped without touching them.
type Store struct {
	db *gorm.DB
}

func New(db *gorm.DB) *Store {
	return &Store{db: db}
}

func newID() string { return uuid.NewString() }

// ---------- Users ----------

func (s *Store) CreateUser(name, email, passwordHash string, role models.Role) (*models.User, error) {
	// Friendly duplicate-email check (DB unique index is the real guard).
	var count int64
	if err := s.db.Model(&models.User{}).Where("email = ?", email).Count(&count).Error; err != nil {
		return nil, err
	}
	if count > 0 {
		return nil, errors.New("email already registered")
	}

	now := time.Now()
	u := &models.User{
		ID:           newID(),
		Name:         name,
		Email:        email,
		PasswordHash: passwordHash,
		Role:         role,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := s.db.Create(u).Error; err != nil {
		return nil, err
	}

	// Auto-create matching empty profile
	switch role {
	case models.RoleDriver:
		_ = s.db.FirstOrCreate(&models.DriverProfile{}, &models.DriverProfile{UserID: u.ID}).Error
	case models.RolePassenger:
		_ = s.db.FirstOrCreate(&models.PassengerProfile{},
			&models.PassengerProfile{UserID: u.ID, FullName: name}).Error
	}
	return u, nil
}

func (s *Store) GetUserByID(id string) (*models.User, bool) {
	var u models.User
	if err := s.db.First(&u, "id = ?", id).Error; err != nil {
		return nil, false
	}
	return &u, true
}

func (s *Store) GetUserByEmail(email string) (*models.User, bool) {
	var u models.User
	if err := s.db.First(&u, "email = ?", email).Error; err != nil {
		return nil, false
	}
	return &u, true
}

func (s *Store) ListUsers(roleFilter string) []*models.User {
	var users []*models.User
	q := s.db.Order("created_at ASC")
	if roleFilter != "" {
		q = q.Where("role = ?", roleFilter)
	}
	_ = q.Find(&users).Error
	return users
}

func (s *Store) UpdateUserRole(id string, role models.Role) (*models.User, error) {
	u, ok := s.GetUserByID(id)
	if !ok {
		return nil, errors.New("user not found")
	}
	u.Role = role
	u.UpdatedAt = time.Now()
	if err := s.db.Save(u).Error; err != nil {
		return nil, err
	}
	// ensure matching profile exists
	switch role {
	case models.RoleDriver:
		_ = s.db.FirstOrCreate(&models.DriverProfile{}, &models.DriverProfile{UserID: id}).Error
	case models.RolePassenger:
		_ = s.db.FirstOrCreate(&models.PassengerProfile{},
			&models.PassengerProfile{UserID: id, FullName: u.Name}).Error
	}
	return u, nil
}

// EnsureDriverProfile returns existing or creates a blank driver profile.
func (s *Store) EnsureDriverProfile(userID string) *models.DriverProfile {
	var p models.DriverProfile
	_ = s.db.FirstOrCreate(&p, &models.DriverProfile{UserID: userID}).Error
	// re-read so all columns are populated
	_ = s.db.First(&p, "user_id = ?", userID).Error
	return &p
}

// EnsurePassengerProfile returns existing or creates a blank passenger profile.
func (s *Store) EnsurePassengerProfile(userID, fallbackName string) *models.PassengerProfile {
	var p models.PassengerProfile
	_ = s.db.FirstOrCreate(&p, &models.PassengerProfile{UserID: userID, FullName: fallbackName}).Error
	_ = s.db.First(&p, "user_id = ?", userID).Error
	return &p
}

func (s *Store) GetDriverProfile(userID string) (*models.DriverProfile, bool) {
	var p models.DriverProfile
	if err := s.db.First(&p, "user_id = ?", userID).Error; err != nil {
		return nil, false
	}
	return &p, true
}

func (s *Store) GetPassengerProfile(userID string) (*models.PassengerProfile, bool) {
	var p models.PassengerProfile
	if err := s.db.First(&p, "user_id = ?", userID).Error; err != nil {
		return nil, false
	}
	return &p, true
}

func (s *Store) UpdateDriverProfile(userID string, fn func(p *models.DriverProfile)) *models.DriverProfile {
	p := s.EnsureDriverProfile(userID)
	fn(p)
	p.UpdatedAt = time.Now()
	_ = s.db.Save(p).Error
	return p
}

func (s *Store) UpdatePassengerProfile(userID string, fn func(p *models.PassengerProfile)) *models.PassengerProfile {
	p := s.EnsurePassengerProfile(userID, "")
	fn(p)
	p.UpdatedAt = time.Now()
	_ = s.db.Save(p).Error
	return p
}

func (s *Store) SetDriverVerified(userID string, verified bool) (*models.DriverProfile, error) {
	p, ok := s.GetDriverProfile(userID)
	if !ok {
		return nil, errors.New("driver profile not found")
	}
	p.Verified = verified
	p.UpdatedAt = time.Now()
	if err := s.db.Save(p).Error; err != nil {
		return nil, err
	}
	return p, nil
}

// ---------- Ratings ----------

func (s *Store) CreateRating(fromUserID, toUserID string, score int, comment string) (*models.Rating, error) {
	if _, ok := s.GetUserByID(toUserID); !ok {
		return nil, errors.New("rated user not found")
	}
	if fromUserID == toUserID {
		return nil, errors.New("cannot rate yourself")
	}
	if score < 1 || score > 5 {
		return nil, errors.New("score must be between 1 and 5")
	}
	r := &models.Rating{
		ID:         newID(),
		FromUserID: fromUserID,
		ToUserID:   toUserID,
		Score:      score,
		Comment:    comment,
		CreatedAt:  time.Now(),
	}
	if err := s.db.Create(r).Error; err != nil {
		return nil, err
	}
	s.recompute(toUserID)
	// re-read not needed; return created rating
	return r, nil
}

// recompute refreshes average/count caches on both profile types.
func (s *Store) recompute(userID string) {
	type agg struct {
		Avg   float64
		Count int64
	}
	var a agg
	_ = s.db.Model(&models.Rating{}).
		Select("COALESCE(AVG(score),0) AS avg, COUNT(*) AS count").
		Where("to_user_id = ?", userID).
		Scan(&a).Error

	_ = s.db.Model(&models.DriverProfile{}).
		Where("user_id = ?", userID).
		Updates(map[string]interface{}{"average_rating": a.Avg, "total_ratings": a.Count}).Error
	_ = s.db.Model(&models.PassengerProfile{}).
		Where("user_id = ?", userID).
		Updates(map[string]interface{}{"average_rating": a.Avg, "total_ratings": a.Count}).Error
}

func (s *Store) ListRatingsForUser(userID string) ([]*models.Rating, float64, int) {
	var ratings []*models.Rating
	_ = s.db.Where("to_user_id = ?", userID).Order("created_at DESC").Find(&ratings).Error
	sum := 0
	for _, r := range ratings {
		sum += r.Score
	}
	avg := 0.0
	if len(ratings) > 0 {
		avg = float64(sum) / float64(len(ratings))
	}
	return ratings, avg, len(ratings)
}
