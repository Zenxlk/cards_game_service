package main

import (
	"log/slog"
	"os"
)

// newLogger configura slog para emitir JSON con una clave "severity" que
// Cloud Logging reconoce nativamente (DEBUG/INFO/WARNING/ERROR/...) — el
// handler de texto por default de slog no le llega con severidad
// reconocible a Cloud Logging (queda con severity vacío en cada entrada),
// así que cualquier alerta basada en severity>=ERROR nunca dispara. Fuera
// de Cloud Run (local, otra plataforma) el JSON sigue siendo un log
// válido, solo menos cómodo a simple vista que el texto plano.
func newLogger() *slog.Logger {
	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.LevelKey {
				level, _ := a.Value.Any().(slog.Level)
				a.Key = "severity"
				a.Value = slog.StringValue(cloudLoggingSeverity(level))
			}
			return a
		},
	})
	return slog.New(handler)
}

// cloudLoggingSeverity traduce los niveles de slog a los nombres de
// severidad que espera Cloud Logging — ver
// https://cloud.google.com/logging/docs/reference/v2/rest/v2/LogEntry#LogSeverity.
func cloudLoggingSeverity(level slog.Level) string {
	switch {
	case level >= slog.LevelError:
		return "ERROR"
	case level >= slog.LevelWarn:
		return "WARNING"
	case level >= slog.LevelInfo:
		return "INFO"
	default:
		return "DEBUG"
	}
}
