// Package execution runs the imported simulator outside the Go API process.
package execution

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"qslate/services/lab/internal/domain"
	"qslate/services/lab/internal/service"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

type Docker struct {
	Config  service.Config
	imageID string
}

func (d *Docker) Preflight(ctx context.Context) error {
	if !d.Config.Execution.Enabled {
		return domain.ErrUnavailable
	}
	runtimeTemplate := fmt.Sprintf(`{{json .Runtimes.%s}}`, d.Config.Execution.Runtime)
	b, e := exec.CommandContext(ctx, "docker", "info", "--format", runtimeTemplate).Output()
	if e != nil || string(bytes.TrimSpace(b)) == "null" || len(bytes.TrimSpace(b)) == 0 {
		return fmt.Errorf("%s runtime unavailable", d.Config.Execution.Runtime)
	}
	b, e = exec.CommandContext(ctx, "docker", "image", "inspect", "--format", "{{.Id}}", d.Config.Execution.Image).Output()
	if e != nil {
		return errors.New("simulator image unavailable; build or provision the configured image")
	}
	id := strings.TrimSpace(string(b))
	if !regexp.MustCompile(`^sha256:[a-f0-9]{64}$`).MatchString(id) {
		return errors.New("invalid image identity")
	}
	d.imageID = id
	return nil
}
func (d *Docker) ImageID() string         { return d.imageID }
func containerName(c domain.Claim) string { return "qslate-lab-" + c.ID + "-" + c.Token }
func (d *Docker) args(c domain.Claim) []string {
	e := d.Config.Execution
	return []string{"create", "--name", containerName(c), "--label", "qslate.lab.managed=true", "--label", "qslate.lab.claim=" + c.Token, "--label", "qslate.lab.run=" + c.ID, "--runtime", e.Runtime, "--network", "none", "--read-only", "--user", "10001:10001", "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--memory", strconv.Itoa(e.MemoryMB) + "m", "--memory-swap", strconv.Itoa(e.MemoryMB) + "m", "--cpus", strconv.Itoa(e.CPUs), "--pids-limit", strconv.Itoa(e.PIDs), "--ulimit", "nofile=64:64", "--ulimit", "core=0:0", "--tmpfs", fmt.Sprintf("/work:rw,noexec,nosuid,nodev,size=%dm,uid=10001,gid=10001,mode=0700", e.WorkMB), "--stop-timeout", "1", "--log-driver", "none", "-i", d.imageID}
}

// Cleanup resolves only this exact claim's labelled container, never a broad prune.
func (d *Docker) Cleanup(ctx context.Context, c domain.Claim) error {
	if !domain.ValidID(c.ID) || !domain.ValidID(c.Token) {
		return domain.ErrInvalid
	}
	b, e := exec.CommandContext(ctx, "docker", "ps", "-aq", "--no-trunc", "--filter", "name=^/"+regexp.QuoteMeta(containerName(c))+"$", "--filter", "label=qslate.lab.managed=true", "--filter", "label=qslate.lab.claim="+c.Token, "--filter", "label=qslate.lab.run="+c.ID).Output()
	if e != nil {
		return e
	}
	for _, id := range strings.Fields(string(b)) {
		if !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(id) {
			return errors.New("invalid container identity")
		}
		if e = exec.CommandContext(ctx, "docker", "rm", "-f", id).Run(); e != nil {
			return e
		}
	}
	return nil
}

type boundedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	remaining := b.limit - b.Len()
	if remaining > len(p) {
		remaining = len(p)
	}
	if remaining > 0 {
		_, _ = b.Buffer.Write(p[:remaining])
	}
	return n, nil
}

var sensitive = regexp.MustCompile(`(?i)(bearer\s+[a-z0-9._~+/-]+|(?:password|secret|token|api[_-]?key)\s*[=:]\s*[^\s,;]+)`)

func clean(s string, max int) string {
	s = sensitive.ReplaceAllString(s, "[REDACTED]")
	s = strings.ToValidUTF8(s, "�")
	if len(s) > max {
		s = s[:max]
		for !utf8.ValidString(s) {
			s = s[:len(s)-1]
		}
	}
	return s
}
func (d *Docker) Run(ctx context.Context, c domain.Claim, progress func(int) error) (domain.Result, error) {
	var result domain.Result
	if !domain.ValidID(c.ID) || !domain.ValidID(c.Token) || d.imageID == "" {
		return result, domain.ErrInvalid
	}
	for _, pin := range []struct{ value, expected string }{{c.Frozen.Source, c.Frozen.SourceSHA256}, {c.Frozen.CSV, c.Frozen.DatasetSHA256}} {
		sum := sha256.Sum256([]byte(pin.value))
		if hex.EncodeToString(sum[:]) != pin.expected {
			return result, errors.New("frozen input checksum mismatch")
		}
	}
	envelope := map[string]any{"job_id": c.ID, "input": c.Frozen.Input, "source": c.Frozen.Source, "csv": c.Frozen.CSV, "max_log_bytes": d.Config.MaxLogBytes}
	input, e := json.Marshal(envelope)
	if e != nil {
		return result, e
	}
	// Create first: cancellation during creation cannot leave a late-started orphan job.
	if _, e = exec.CommandContext(ctx, "docker", d.args(c)...).Output(); e != nil {
		return result, e
	}
	if e = ctx.Err(); e != nil {
		return result, e
	}
	cmd := exec.CommandContext(ctx, "docker", "start", "--attach", "--interactive", containerName(c))
	cmd.Stdin = bytes.NewReader(input)
	stderr := &boundedBuffer{limit: 4096}
	cmd.Stderr = stderr
	stdout, e := cmd.StdoutPipe()
	if e != nil {
		return result, e
	}
	if e = cmd.Start(); e != nil {
		return result, e
	}
	// A bounded reader caps total protocol output, including deliberately noisy strategies.
	scanner := bufio.NewScanner(io.LimitReader(stdout, int64(d.Config.MaxArtifactBytes)+65537))
	scanner.Buffer(make([]byte, 4096), d.Config.MaxArtifactBytes+1)
	total := 0
	found := false
	var protocolErr error
	for scanner.Scan() {
		line := scanner.Bytes()
		total += len(line) + 1
		if total > d.Config.MaxArtifactBytes+65536 {
			protocolErr = errors.New("artifact output limit exceeded")
			break
		}
		var event struct {
			Type     string        `json:"type"`
			Progress int           `json:"progress"`
			Result   domain.Result `json:"result"`
		}
		if e = json.Unmarshal(line, &event); e != nil {
			protocolErr = errors.New("invalid simulator protocol")
			break
		}
		switch event.Type {
		case "progress":
			if found || event.Progress < 0 || event.Progress > 99 {
				protocolErr = errors.New("invalid simulator progress")
			} else {
				protocolErr = progress(event.Progress)
			}
		case "result":
			if found || len(line) > d.Config.MaxArtifactBytes || (event.Result.Status != "completed" && event.Result.Status != "failed") {
				protocolErr = errors.New("invalid simulator result")
			} else {
				result = event.Result
				found = true
			}
		default:
			protocolErr = errors.New("unsupported simulator event")
		}
		if protocolErr != nil {
			break
		}
	}
	if e = scanner.Err(); e != nil && protocolErr == nil {
		protocolErr = e
	}
	if protocolErr != nil {
		_ = cmd.Process.Kill()
	}
	waitErr := cmd.Wait()
	if ctx.Err() != nil {
		return domain.Result{}, ctx.Err()
	}
	if protocolErr != nil {
		return domain.Result{}, protocolErr
	}
	if waitErr != nil || !found {
		return domain.Result{}, errors.New("simulator process did not return artifacts")
	}
	if len(result.Trades) > d.Config.MaxDatasetRows*100 || len(result.Equity) > d.Config.MaxDatasetRows || len(result.Telemetry) > d.Config.MaxDatasetRows {
		return domain.Result{}, errors.New("artifact row limit exceeded")
	}
	result.Logs = clean(result.Logs, d.Config.MaxLogBytes)
	result.Error = clean(result.Error, 512)
	if len(result.Summary) == 0 || !json.Valid(result.Summary) {
		return domain.Result{}, errors.New("missing simulator summary")
	}
	return result, nil
}
