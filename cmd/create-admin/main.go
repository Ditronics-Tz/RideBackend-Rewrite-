package main

import (
	"bufio"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"ride-backend/config"
	"ride-backend/db"
	"ride-backend/models"
	"ride-backend/store"

	"golang.org/x/crypto/bcrypt"
)

func main() {
	emailFlag := flag.String("email", "", "Admin email address")
	nameFlag := flag.String("name", "", "Admin full name")
	flag.Parse()

	email := strings.ToLower(strings.TrimSpace(*emailFlag))
	name := strings.TrimSpace(*nameFlag)

	if email == "" || name == "" {
		fmt.Fprintf(os.Stderr, "Usage: go run cmd/create-admin/main.go --email <email> --name <name>\n")
		os.Exit(1)
	}

	cfg := config.LoadConfig()
	gdb := db.Connect(cfg)
	appStore := store.New(gdb)

	// Refuse to run if any active admin already exists
	activeAdminCount := appStore.CountActiveAdmins()
	if activeAdminCount > 0 {
		log.Fatalf("Error: Refusing to create bootstrap admin. System already has %d active admin account(s).", activeAdminCount)
	}

	// Read password from STAFF_BOOTSTRAP_PASSWORD or stdin
	password := strings.TrimSpace(os.Getenv("STAFF_BOOTSTRAP_PASSWORD"))
	if password == "" {
		fmt.Print("Enter admin password (min 12 characters): ")
		reader := bufio.NewReader(os.Stdin)
		input, err := reader.ReadString('\n')
		if err != nil {
			log.Fatalf("Failed to read password from stdin: %v", err)
		}
		password = strings.TrimSpace(input)
	}

	if len(password) < 12 {
		log.Fatalf("Error: Password must be at least 12 characters long.")
	}

	hashed, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		log.Fatalf("Failed to hash password: %v", err)
	}

	admin, err := appStore.CreateStaffUser(name, email, string(hashed), models.StaffRoleAdmin, nil, false)
	if err != nil {
		log.Fatalf("Failed to create admin account: %v", err)
	}

	_ = appStore.CreateAuditLog(admin.ID, "bootstrap_admin_created", "staff_user", admin.ID, "127.0.0.1", nil)

	fmt.Printf("\nBootstrap admin successfully created!\n")
	fmt.Printf("ID:    %s\n", admin.ID)
	fmt.Printf("Name:  %s\n", admin.Name)
	fmt.Printf("Email: %s\n", admin.Email)
	fmt.Printf("Role:  %s\n", admin.Role)
}
