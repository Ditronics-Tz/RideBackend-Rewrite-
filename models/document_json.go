package models

import "encoding/json"

// Object-backed rows persist the object key. The protected API URL is derived
// at serialization time, so expiring signed URLs never enter the database.
func (d DriverDocument) MarshalJSON() ([]byte, error) {
	type document DriverDocument
	value := document(d)
	if d.ObjectKey != "" {
		value.FileURL = "/api/v1/files/documents/" + d.ID
	}
	return json.Marshal(value)
}

func (p DriverProfile) MarshalJSON() ([]byte, error) {
	type profile DriverProfile
	value := profile(p)
	if p.ProfilePictureKey != "" && value.ProfilePictureURL == "" {
		value.ProfilePictureURL = "/api/v1/profile-images/" + p.UserID
	}
	return json.Marshal(value)
}

func (p PassengerProfile) MarshalJSON() ([]byte, error) {
	type profile PassengerProfile
	value := profile(p)
	if p.ProfilePictureKey != "" && value.ProfilePictureURL == "" {
		value.ProfilePictureURL = "/api/v1/profile-images/" + p.UserID
	}
	var photoURL *string
	if value.ProfilePictureURL != "" {
		photoURL = &value.ProfilePictureURL
	}
	return json.Marshal(struct {
		profile
		ProfilePictureURL *string `json:"profile_picture_url"`
	}{value, photoURL})
}
