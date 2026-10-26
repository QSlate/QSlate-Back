package contracts

import (
	"encoding/json"
	"os"
	"path/filepath"
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
		AuthRequired bool   `json:"auth_required"`
		Upstream     string `json:"upstream"`
		TargetPath   string `json:"target_path"`
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
		if (strings.HasPrefix(route.Path, "/qslate/v1/users/") || strings.HasPrefix(route.Path, "/qslate/v1/dashboards")) && !route.AuthRequired {
			t.Fatalf("account route unprotected: %s", key)
		}
		if strings.HasPrefix(route.Path, "/qslate/v1/dashboards") && (route.Upstream != "${WORKSPACE_SERVICE_URL}" || route.TargetPath != strings.TrimPrefix(route.Path, "/qslate/v1")) {
			t.Fatalf("invalid Workspace route mapping: %s", key)
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
	if count != 98 || implemented != 28 {
		t.Fatalf("REST operations: %d total / %d implemented, expected 95+3 / 14+3+11", count, implemented)
	}
}

func TestWorkspaceViewsReferenceEveryCanonicalPath(t *testing.T) {
	for _, path := range []string{"../../docs/workspace.openapi.yaml", "../../services/workspace/openapi.yaml"} {
		var view struct {
			Paths map[string]struct {
				Ref string `json:"$ref"`
			}
		}
		readJSON(t, path, &view)
		paths := 0
		for _, entry := range view.Paths {
			if entry.Ref == "" {
				continue
			}
			parts := strings.SplitN(entry.Ref, "#", 2)
			if len(parts) != 2 {
				t.Fatal("missing canonical pointer")
			}
			var canonical struct {
				Paths map[string]map[string]struct {
					Implementation string `json:"x-implementation"`
				}
			}
			readJSON(t, filepath.Join(filepath.Dir(path), parts[0]), &canonical)
			key := strings.ReplaceAll(strings.ReplaceAll(strings.TrimPrefix(parts[1], "/paths/"), "~1", "/"), "~0", "~")
			if !strings.HasPrefix(key, "/qslate/v1/dashboards") || len(canonical.Paths[key]) == 0 {
				t.Fatalf("invalid Workspace reference: %s", entry.Ref)
			}
			for _, op := range canonical.Paths[key] {
				if op.Implementation != "implemented" {
					t.Fatal("Workspace view contains planned operation")
				}
			}
			paths++
		}
		if paths != 7 {
			t.Fatalf("expected 7 paths / 11 operations in %s, got %d paths", path, paths)
		}
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
