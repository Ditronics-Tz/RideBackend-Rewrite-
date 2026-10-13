package postman_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestCollectionAuthorizationVariables(t *testing.T) {
	data, err := os.ReadFile("ride-backend.postman_collection.json")
	if err != nil {
		t.Fatal(err)
	}
	type header struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	type item struct {
		Name    string            `json:"name"`
		Items   []json.RawMessage `json:"item"`
		Request struct {
			Headers []header `json:"header"`
		} `json:"request"`
	}
	var check func(json.RawMessage)
	check = func(raw json.RawMessage) {
		t.Helper()
		var node item
		if err := json.Unmarshal(raw, &node); err != nil {
			t.Fatal(err)
		}
		for _, h := range node.Request.Headers {
			if strings.EqualFold(h.Key, "Authorization") && strings.Contains(h.Value, "{{") && !strings.HasSuffix(h.Value, "}}") {
				t.Errorf("%s: malformed authorization variable %q", node.Name, h.Value)
			}
		}
		for _, child := range node.Items {
			check(child)
		}
	}
	check(data)
}
