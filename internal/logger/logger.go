package logger

import (
	"io"
	"os"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/pkgerrors"
)

var Log zerolog.Logger

type Config struct {
	Level       string
	Format      string
	ServiceName string
	Version     string
}

func Init(cfg Config) {
	// Enable stack traces for errors
	zerolog.ErrorStackMarshaler = pkgerrors.MarshalStack
	zerolog.TimeFieldFormat = time.RFC3339Nano

	// Set log level
	level, err := zerolog.ParseLevel(cfg.Level)
	if err != nil {
		level = zerolog.InfoLevel
	}
	zerolog.SetGlobalLevel(level)

	// Configure output format
	var output io.Writer = os.Stdout
	if cfg.Format == "text" {
		output = zerolog.ConsoleWriter{
			Out:        os.Stdout,
			TimeFormat: "15:04:05",
		}
	}

	// Create logger with context
	Log = zerolog.New(output).
		With().
		Timestamp().
		Str("service", cfg.ServiceName).
		Str("version", cfg.Version).
		Logger()
}

// Helper functions for common logging patterns
func Info() *zerolog.Event {
	return Log.Info()
}

func Error() *zerolog.Event {
	return Log.Error()
}

func Debug() *zerolog.Event {
	return Log.Debug()
}

func Warn() *zerolog.Event {
	return Log.Warn()
}

func Fatal() *zerolog.Event {
	return Log.Fatal()
}

func WithRequestID(requestID string) zerolog.Logger {
	return Log.With().Str("request_id", requestID).Logger()
}
