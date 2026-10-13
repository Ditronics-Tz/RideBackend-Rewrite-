package main

import (
	"fmt"
	"os"
	"path/filepath"

	"ride-backend/apidoc"
)

func main() {
	if err := generate(); err != nil {
		fmt.Fprintln(os.Stderr, "API documentation generation failed")
		os.Exit(1)
	}
}

func generate() error {
	spec, err := apidoc.Generate()
	if err != nil {
		return err
	}
	guards, err := apidoc.GenerateRouteGuards()
	if err != nil {
		return err
	}
	for _, file := range []struct {
		path string
		body []byte
	}{{"docs/api/openapi.yaml", spec}, {"docs/ship/route-guards.md", guards}} {
		if err := os.MkdirAll(filepath.Dir(file.path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(file.path, file.body, 0o644); err != nil {
			return err
		}
	}
	fmt.Println("Generated docs/api/openapi.yaml and docs/ship/route-guards.md offline")
	return nil
}
