package contracts

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func readJSON(t *testing.T, path string, out any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(data, out); err != nil {
		t.Fatal(err)
	}
}

func TestGatewayPublishesOnlyImplementedContract(t *testing.T) {
	var spec struct {
		Paths map[string]map[string]struct {
			Implementation string                `json:"x-implementation"`
			OperationID    string                `json:"operationId"`
			Security       []map[string][]string `json:"security"`
		}
	}
	readJSON(t, "../../docs/qslate.openapi.json", &spec)
	var routes []struct {
		Method, Path string
		AuthRequired bool `json:"auth_required"`
	}
	readJSON(t, "../../gateway/config/routes.json", &routes)
	published := map[string]bool{}
	for _, route := range routes {
		key := route.Method + " " + route.Path
		if published[key] {
			t.Fatalf("duplicate route %s", key)
		}
		published[key] = true
		operation, found := spec.Paths[route.Path][strings.ToLower(route.Method)]
		if !found || operation.Implementation != "implemented" {
			t.Fatalf("route absent or planned in contract: %s", key)
		}
		if strings.HasPrefix(route.Path, "/qslate/v1/users/") && !route.AuthRequired {
			t.Fatalf("account route unprotected: %s", key)
		}
	}
	count, implemented := 0, 0
	ids := map[string]bool{}
	for path, methods := range spec.Paths {
		for method, operation := range methods {
			if !strings.Contains(" get post put patch delete options head ", " "+method+" ") {
				continue
			}
			count++
			if operation.OperationID == "" || ids[operation.OperationID] {
				t.Fatalf("missing or duplicate operationId: %s %s", method, path)
			}
			ids[operation.OperationID] = true
			switch operation.Implementation {
			case "implemented":
				implemented++
				if !published[strings.ToUpper(method)+" "+path] {
					t.Fatalf("implemented operation not routed: %s %s", method, path)
				}
			case "planned":
			default:
				t.Fatalf("missing implementation status: %s %s", method, path)
			}
		}
	}
	if count != 98 || implemented != 17 {
		t.Fatalf("REST operations: %d total / %d implemented, expected 95+3 / 14+3", count, implemented)
	}
}

func TestPlannedStreamsAndLocalReferences(t *testing.T) {
	for _, path := range []string{"../../docs/qslate.openapi.json", "../../docs/streams.asyncapi.json"} {
		var spec map[string]any
		readJSON(t, path, &spec)
		if strings.Contains(path, "asyncapi") {
			if channels, ok := spec["channels"].(map[string]any); !ok || len(channels) != 2 {
				t.Fatal("expected two planned WS channels")
			}
		}
		var walk func(any)
		walk = func(node any) {
			switch value := node.(type) {
			case []any:
				for _, item := range value {
					walk(item)
				}
			case map[string]any:
				if ref, ok := value["$ref"].(string); ok && strings.HasPrefix(ref, "#/") {
					var target any = spec
					for _, part := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
						object, ok := target.(map[string]any)
						if !ok {
							t.Fatalf("invalid reference %s in %s", ref, path)
						}
						target, ok = object[strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")]
						if !ok {
							t.Fatalf("unresolved reference %s in %s", ref, path)
						}
					}
				}
				for _, item := range value {
					walk(item)
				}
			}
		}
		walk(spec)
	}
}
