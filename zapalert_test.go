package zapalert

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/your_github_user_or_org/zapalert/ctxmeta"
)

func TestLogSchemaKeys(t *testing.T) {
	logFile, err := os.CreateTemp(t.TempDir(), "zapalert-*.log")
	if err != nil {
		t.Fatalf("CreateTemp() error = %v", err)
	}
	logPath := logFile.Name()
	_ = logFile.Close()

	logger, err := New(
		WithServiceName("kyc-service"),
		WithOutputPaths([]string{logPath}),
		WithErrorOutputPaths([]string{logPath}),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ctx := ctxmeta.WithRequestID(context.Background(), "req-123")
	logger.Info(ctx, "KYC.Submit", "request accepted")
	logger.Error(ctx, "KYC.Submit", errors.New("bad request"), "request failed")
	if err := logger.Sync(); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}

	f, err := os.Open(logPath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	if !scanner.Scan() {
		t.Fatalf("expected first log line")
	}
	first := parseJSONLine(t, scanner.Bytes())
	assertField(t, first, "ts")
	assertField(t, first, "level")
	assertField(t, first, "alert_level")
	assertField(t, first, "msg")
	assertField(t, first, "service")
	assertField(t, first, "method")
	assertField(t, first, "request_id")
	if _, exists := first["err"]; exists {
		t.Fatalf("first log unexpectedly contains err field")
	}

	if !scanner.Scan() {
		t.Fatalf("expected second log line")
	}
	second := parseJSONLine(t, scanner.Bytes())
	assertField(t, second, "err")
	if _, exists := second["error"]; exists {
		t.Fatalf("second log unexpectedly contains error key; want err")
	}
	if got, _ := second["alert_level"].(string); got != "NONE" {
		t.Fatalf("alert_level = %q, want NONE", got)
	}
}

func parseJSONLine(t *testing.T, line []byte) map[string]any {
	t.Helper()
	var parsed map[string]any
	if err := json.Unmarshal(line, &parsed); err != nil {
		t.Fatalf("json.Unmarshal() error = %v, line=%s", err, string(line))
	}
	return parsed
}

func assertField(t *testing.T, payload map[string]any, key string) {
	t.Helper()
	if _, ok := payload[key]; !ok {
		t.Fatalf("expected key %q in payload: %#v", key, payload)
	}
}
