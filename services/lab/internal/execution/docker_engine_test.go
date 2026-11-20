//go:build engine

package execution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"qslate/services/lab/internal/domain"
	"qslate/services/lab/internal/service"
	"strings"
	"testing"
	"time"
)

// Explicit opt-in tests execute only fixed repository-owned fixtures with runc.
// Production LoadConfig categorically rejects runc; no request can select it.
func TestRealPythonEngineThroughGoDockerTransport(t *testing.T) {
	image := os.Getenv("SIMULATOR_TEST_IMAGE")
	if image == "" {
		t.Fatal("SIMULATOR_TEST_IMAGE required for trusted-fixture engine tests")
	}
	raw, e := exec.Command("docker", "image", "inspect", "--format", "{{.Id}}", image).Output()
	if e != nil {
		t.Fatal(e)
	}
	cfg, e := service.LoadConfig("../../config/lab.json")
	if e != nil {
		t.Fatal(e)
	}
	cfg.Execution.Runtime = "runc"
	runner := Docker{Config: cfg, imageID: strings.TrimSpace(string(raw))}
	csv := "Datetime,Open,High,Low,Close,Volume\n"
	for i := 0; i < 30; i++ {
		ts := time.Date(2024, 1, 1, i, 0, 0, 0, time.UTC).Format(time.RFC3339)
		csv += ts + ",100,102,99,101,100\n"
	}
	source := "from simulator import Strategy\nimport os\nclass Trusted(Strategy):\n def on_bar(self, ctx):\n  assert os.getenv('DATABASE_URL') is None\n  ctx.record(price=ctx.price)\n  if not ctx.has_position: ctx.buy(usd_amount=1000)\n"
	claim := domain.Claim{ID: domain.ID(), Token: domain.ID(), Frozen: domain.Frozen{Input: domain.RunInput{InitialCapital: "10000", Window: 5}, Source: source, CSV: csv}}
	sum := sha256.Sum256([]byte(source))
	claim.Frozen.SourceSHA256 = hex.EncodeToString(sum[:])
	sum = sha256.Sum256([]byte(csv))
	claim.Frozen.DatasetSHA256 = hex.EncodeToString(sum[:])
	cleanup := func(c domain.Claim) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if e := runner.Cleanup(ctx, c); e != nil {
			t.Error(e)
		}
	}
	defer cleanup(claim)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	progress := 0
	result, e := runner.Run(ctx, claim, func(v int) error {
		if v > progress {
			progress = v
		}
		return nil
	})
	if e != nil || result.Status != "completed" || len(result.Trades) == 0 || len(result.Equity) != 25 || len(result.Telemetry) != 25 || progress == 0 {
		t.Fatalf("real engine: %+v %v, progress %d", result, e, progress)
	}
	blocked := domain.Claim{ID: domain.ID(), Token: domain.ID(), Frozen: claim.Frozen}
	blocked.Frozen.Source = "while True: pass"
	sum = sha256.Sum256([]byte(blocked.Frozen.Source))
	blocked.Frozen.SourceSHA256 = hex.EncodeToString(sum[:])
	defer cleanup(blocked)
	deadline, stop := context.WithTimeout(context.Background(), 2*time.Second)
	defer stop()
	if _, e = runner.Run(deadline, blocked, func(int) error { return nil }); e == nil {
		t.Fatal("infinite strategy ignored deadline")
	}
}
