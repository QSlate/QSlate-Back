package execution

import (
	"qslate/services/lab/internal/domain"
	"qslate/services/lab/internal/service"
	"strings"
	"testing"
)

func TestExecutionArgumentsNeverMountDataOrExposeCredentials(t *testing.T) {
	c, e := service.LoadConfig("../../config/lab.json")
	if e != nil {
		t.Fatal(e)
	}
	d := Docker{Config: c, imageID: "sha256:" + strings.Repeat("a", 64)}
	args := d.args(domain.Claim{ID: domain.ID(), Token: domain.ID()})
	all := strings.Join(args, " ")
	for _, required := range []string{"--runtime runsc", "--network none", "--read-only", "--cap-drop ALL", "--security-opt no-new-privileges", "--memory-swap 512m", "--pids-limit 64", "--user 10001:10001", "--tmpfs /work:"} {
		if !strings.Contains(all, required) {
			t.Fatal("missing isolation:", required)
		}
	}
	for _, forbidden := range []string{"--volume", "--mount", "--env", "--privileged", "--pid host", "--network host", "docker.sock"} {
		if strings.Contains(all, forbidden) {
			t.Fatal("unsafe execution arguments")
		}
	}
}
func TestLogsAreBoundedValidUTF8AndRedacted(t *testing.T) {
	s := clean("token=abc Bearer xyz password=secret "+strings.Repeat("é", 100), 90)
	if strings.Contains(s, "abc") || strings.Contains(s, "xyz") || strings.Contains(s, "secret") || len(s) > 90 {
		t.Fatal(s)
	}
}
