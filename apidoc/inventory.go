// Package apidoc builds the checked-in API inventory without requests, filesystem
// uploads, environment configuration or a live database connection.
package apidoc

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/gofiber/fiber/v2"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"ride-backend/config"
	"ride-backend/routes"
	"ride-backend/store"
)

type Route struct {
	Method, Path, OpenAPIPath, Handler string
	Parameters                         []string
}

type offlineDriver struct{}

func (offlineDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("API inventory cannot connect to a database")
}

var registerDriver sync.Once

// Inventory excludes Fiber middleware registrations with GetRoutes(true),
// implicit HEAD mirrors and framework OPTIONS handlers. Concrete static GET
// routes remain included, with wildcard paths represented as {path}.
func Inventory() ([]Route, error) {
	registerDriver.Do(func() { sql.Register("rida-offline-inventory", offlineDriver{}) })
	database, err := sql.Open("rida-offline-inventory", "")
	if err != nil {
		return nil, err
	}
	defer database.Close()
	gdb, err := gorm.Open(postgres.New(postgres.Config{Conn: database}), &gorm.Config{DryRun: true, DisableAutomaticPing: true, Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		return nil, err
	}
	// No local adapter is constructed. Route registration neither needs an R2
	// client nor authentication signing secrets; handlers are never executed.
	app := routes.BuildApp(&config.Config{StorageDriver: "r2", PaymentProviderName: "mock"}, store.New(gdb))
	return inventoryFor(app), nil
}

func inventoryFor(app *fiber.App) []Route {
	seen := map[string]bool{}
	inventory := []Route{}
	for _, route := range app.GetRoutes(true) {
		if route.Method == "HEAD" || route.Method == "OPTIONS" {
			continue
		}
		key := route.Method + " " + route.Path
		if seen[key] {
			continue
		}
		seen[key] = true
		parts := strings.Split(route.Path, "/")
		parameters := []string{}
		for i, part := range parts {
			if strings.HasPrefix(part, ":") {
				name := strings.TrimSuffix(strings.TrimPrefix(part, ":"), "?")
				parameters = append(parameters, name)
				parts[i] = "{" + name + "}"
			}
			if part == "*" {
				parameters = append(parameters, "path")
				parts[i] = "{path}"
			}
		}
		handler := ""
		if len(route.Handlers) > 0 {
			if fn := runtime.FuncForPC(reflect.ValueOf(route.Handlers[len(route.Handlers)-1]).Pointer()); fn != nil {
				handler = strings.TrimSuffix(fn.Name(), "-fm")
			}
		}
		inventory = append(inventory, Route{Method: route.Method, Path: route.Path, OpenAPIPath: strings.Join(parts, "/"), Handler: handler, Parameters: parameters})
	}
	sort.Slice(inventory, func(i, j int) bool {
		if inventory[i].Path == inventory[j].Path {
			return inventory[i].Method < inventory[j].Method
		}
		return inventory[i].Path < inventory[j].Path
	})
	return inventory
}
