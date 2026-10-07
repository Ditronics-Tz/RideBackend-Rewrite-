package models_test

import (
	"testing"

	"ride-backend/models"
)

func TestParseRole(t *testing.T) {
	t.Run("Valid app roles succeed", func(t *testing.T) {
		cases := []struct {
			input    string
			expected models.Role
		}{
			{"driver", models.RoleDriver},
			{"DRIVER", models.RoleDriver},
			{" dereva ", models.RoleDriver},
			{"passenger", models.RolePassenger},
			{"PASSENGER", models.RolePassenger},
			{" abiria ", models.RolePassenger},
		}

		for _, tc := range cases {
			r, err := models.ParseRole(tc.input)
			if err != nil {
				t.Errorf("Unexpected error for %q: %v", tc.input, err)
			}
			if r != tc.expected {
				t.Errorf("For %q expected %q, got %q", tc.input, tc.expected, r)
			}
		}
	})

	t.Run("Unknown roles are strictly rejected with an error", func(t *testing.T) {
		invalidInputs := []string{
			"admin",
			"support",
			"mzee",
			"superuser",
			"root",
			"guest",
			"",
			"   ",
			"random_role",
		}

		for _, input := range invalidInputs {
			r, err := models.ParseRole(input)
			if err == nil {
				t.Errorf("Expected error for invalid role %q, got parsed role %q", input, r)
			}
		}
	})
}

func TestParseStaffRole(t *testing.T) {
	t.Run("Valid staff roles succeed", func(t *testing.T) {
		r1, err := models.ParseStaffRole("admin")
		if err != nil || r1 != models.StaffRoleAdmin {
			t.Errorf("Expected admin, got %q, err: %v", r1, err)
		}

		r2, err := models.ParseStaffRole("support")
		if err != nil || r2 != models.StaffRoleSupport {
			t.Errorf("Expected support, got %q, err: %v", r2, err)
		}
	})

	t.Run("Invalid staff roles rejected", func(t *testing.T) {
		for _, input := range []string{"driver", "passenger", "mzee", "superadmin", ""} {
			_, err := models.ParseStaffRole(input)
			if err == nil {
				t.Errorf("Expected error for invalid staff role %q", input)
			}
		}
	})
}
