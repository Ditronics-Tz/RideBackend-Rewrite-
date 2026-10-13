package apidoc

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
)

func readDocument(t *testing.T) object {
	t.Helper()
	body, err := os.ReadFile("../docs/api/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var document object
	if err := json.Unmarshal(body, &document); err != nil {
		t.Fatal("generated YAML must also be valid JSON")
	}
	return document
}

func TestEveryConcreteFiberRouteHasCommittedSpecEntry(t *testing.T) {
	document := readDocument(t)
	paths := document["paths"].(map[string]any)
	inventory, err := Inventory()
	if err != nil {
		t.Fatal(err)
	}
	actual := map[string]bool{}
	for _, route := range inventory {
		actual[strings.ToLower(route.Method)+" "+route.OpenAPIPath] = true
		path, ok := paths[route.OpenAPIPath].(map[string]any)
		if !ok {
			t.Errorf("route has no OpenAPI path: %s %s", route.Method, route.Path)
			continue
		}
		if _, exists := path[strings.ToLower(route.Method)]; !exists {
			t.Errorf("route has no OpenAPI operation: %s %s", route.Method, route.Path)
		}
	}
	for path, methods := range paths {
		for method := range methods.(map[string]any) {
			if !actual[method+" "+path] {
				t.Errorf("spec documents an unregistered route: %s %s", method, path)
			}
		}
	}
}

func TestGeneratedSpecAndGuardInventoryAreDeterministicAndCurrent(t *testing.T) {
	for _, artifact := range []struct {
		path     string
		generate func() ([]byte, error)
	}{{"../docs/api/openapi.yaml", Generate}, {"../docs/ship/route-guards.md", GenerateRouteGuards}} {
		first, err := artifact.generate()
		if err != nil {
			t.Fatal(err)
		}
		second, err := artifact.generate()
		if err != nil {
			t.Fatal(err)
		}
		committed, err := os.ReadFile(artifact.path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(first, second) || !bytes.Equal(first, committed) {
			t.Fatalf("%s is stale or nondeterministic; run go run ./cmd/generate-openapi", artifact.path)
		}
	}
}

func TestInventoryExplicitlyExcludesMiddlewareHeadAndOptions(t *testing.T) {
	app := fiber.New()
	app.Use("/middleware/:id", func(c *fiber.Ctx) error { return c.Next() })
	app.Get("/real/:id", func(c *fiber.Ctx) error { return nil })
	app.Options("/real/:id", func(c *fiber.Ctx) error { return nil })
	inventory := inventoryFor(app)
	if len(inventory) != 1 || inventory[0].Method != "GET" || inventory[0].OpenAPIPath != "/real/{id}" {
		t.Fatalf("framework/middleware route leaked into inventory: %+v", inventory)
	}
}

func TestSpecReferencesSecurityAndPathParametersResolve(t *testing.T) {
	document := readDocument(t)
	components := document["components"].(map[string]any)
	paths := document["paths"].(map[string]any)
	ids := map[string]bool{}
	var checkRefs func(any)
	checkRefs = func(value any) {
		switch value := value.(type) {
		case map[string]any:
			if reference, ok := value["$ref"].(string); ok {
				parts := strings.Split(reference, "/")
				if len(parts) != 4 || parts[0] != "#" || parts[1] != "components" {
					t.Errorf("invalid internal reference %s", reference)
				} else if collection, ok := components[parts[2]].(map[string]any); !ok || collection[parts[3]] == nil {
					t.Errorf("unresolved internal reference %s", reference)
				}
			}
			for _, child := range value {
				checkRefs(child)
			}
		case []any:
			for _, child := range value {
				checkRefs(child)
			}
		}
	}
	checkRefs(document)
	for path, methods := range paths {
		for method, value := range methods.(map[string]any) {
			operation := value.(map[string]any)
			id := operation["operationId"].(string)
			if ids[id] {
				t.Errorf("duplicate operation ID %s", id)
			}
			ids[id] = true
			if len(operation["responses"].(map[string]any)) == 0 {
				t.Errorf("missing responses: %s %s", method, path)
			}
			parameters := map[string]bool{}
			for _, parameter := range operation["parameters"].([]any) {
				p := parameter.(map[string]any)
				if p["in"] == "path" {
					if p["required"] != true {
						t.Error("path parameter must be required")
					}
					parameters[p["name"].(string)] = true
				}
			}
			for _, part := range strings.Split(path, "/") {
				if strings.HasPrefix(part, "{") && !parameters[strings.Trim(part, "{}")] {
					t.Errorf("missing path parameter %s", part)
				}
			}
			for _, security := range operation["security"].([]any) {
				for name := range security.(map[string]any) {
					if components["securitySchemes"].(map[string]any)[name] == nil {
						t.Errorf("unknown security scheme %s", name)
					}
				}
			}
		}
	}
}

func TestCurrentContractRetainsActualKeysAndLabelsPlaceholders(t *testing.T) {
	document := readDocument(t)
	paths := document["paths"].(map[string]any)
	get := func(path, method string) map[string]any { return paths[path].(map[string]any)[method].(map[string]any) }
	audit := get("/api/v1/admin/audit-logs", "get")
	schema := audit["responses"].(map[string]any)["200"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
	if schema["properties"].(map[string]any)["logs"] == nil {
		t.Fatal("audit logs collection key was changed in docs")
	}
	for _, path := range []string{"/api/v1/admin/rides", "/api/v1/admin/tickets"} {
		operation := get(path, "get")
		if operation["x-implementation-status"] != "unavailable-until-B8" || operation["responses"].(map[string]any)["501"] == nil || operation["responses"].(map[string]any)["200"] != nil {
			t.Fatal("unavailable workflow was documented as persisted")
		}
	}
	if get("/api/v1/admin/tickets/{id}", "patch")["x-implementation-status"] != "unavailable-until-B8" {
		t.Fatal("ticket update persistence was promised")
	}
	for _, path := range []string{"/api/v1/driver/profile/{userId}", "/api/v1/passenger/profile/{userId}"} {
		operation := get(path, "get")
		if operation["x-privacy-contract"] != "public DTO for other users; full profile for owner" {
			t.Fatal("public profile DTO contract is missing")
		}
	}
	publicDriver := get("/api/v1/driver/profile/{userId}", "get")["responses"].(map[string]any)["200"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
	if publicDriver["$ref"] != "#/components/schemas/PublicDriverProfile" {
		t.Fatal("cross-user driver lookup does not use its public DTO")
	}
	for _, path := range []string{"/health", "/ready", "/api/v1/health", "/api/v1/ready"} {
		if paths[path] == nil {
			t.Fatal("runtime alias missing")
		}
	}
	schemas := document["components"].(map[string]any)["schemas"].(map[string]any)
	for _, name := range []string{"DriverDocument", "DriverProfile", "PassengerProfile"} {
		fields := schemas[name].(map[string]any)["properties"].(map[string]any)
		if fields["object_key"] != nil || fields["staging_object_key"] != nil || fields["profile_picture_key"] != nil || fields["file_path"] != nil {
			t.Fatal("private object metadata appears in API schema")
		}
	}
	publicFields := schemas["PublicDriverProfile"].(map[string]any)["properties"].(map[string]any)
	for _, sensitive := range []string{"phone", "licence_number", "vehicle_card_number", "plate_number", "licence_image_url"} {
		if publicFields[sensitive] != nil {
			t.Fatalf("public driver schema exposes %s", sensitive)
		}
	}
}
