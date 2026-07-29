package main

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"
)

func TestCloudLoggingSeverity(t *testing.T) {
	cases := []struct {
		level slog.Level
		want  string
	}{
		{slog.LevelDebug, "DEBUG"},
		{slog.LevelInfo, "INFO"},
		{slog.LevelWarn, "WARNING"},
		{slog.LevelError, "ERROR"},
	}
	for _, c := range cases {
		if got := cloudLoggingSeverity(c.level); got != c.want {
			t.Errorf("cloudLoggingSeverity(%v) = %q, quería %q", c.level, got, c.want)
		}
	}
}

// TestNewLogger_EmitsSeverityKeyCloudLoggingRecognizes confirma que el
// nivel de slog termina en una clave "severity" (no "level") con uno de
// los valores que Cloud Logging reconoce — sin esto, las alertas basadas
// en severidad nunca disparan (ver el comentario en newLogger).
func TestNewLogger_EmitsSeverityKeyCloudLoggingRecognizes(t *testing.T) {
	var buf bytes.Buffer
	handler := slog.NewJSONHandler(&buf, &slog.HandlerOptions{
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.LevelKey {
				level, _ := a.Value.Any().(slog.Level)
				a.Key = "severity"
				a.Value = slog.StringValue(cloudLoggingSeverity(level))
			}
			return a
		},
	})
	logger := slog.New(handler)
	logger.Error("algo falló", "err", "boom")

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("la salida no es JSON válido: %v\nsalida: %s", err, buf.String())
	}
	if _, hasLevel := entry["level"]; hasLevel {
		t.Error("no debería quedar una clave 'level' — Cloud Logging espera 'severity'")
	}
	if got := entry["severity"]; got != "ERROR" {
		t.Errorf("severity = %v, quería \"ERROR\"", got)
	}
}
