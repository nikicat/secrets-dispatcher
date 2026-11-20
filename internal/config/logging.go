package config

import "log/slog"

// The lowest severity of log record that is written. Less severe records are
// dropped.
type LogLevel string

const (
	LogDebug LogLevel = "debug"
	LogInfo  LogLevel = "info"
	LogWarn  LogLevel = "warn"
	LogError LogLevel = "error"
)

// The valid log levels, from most to least verbose.
var LogLevels = NewEnum("log level", LogDebug, LogInfo, LogWarn, LogError)

var slogLevels = map[LogLevel]slog.Level{
	LogDebug: slog.LevelDebug,
	LogInfo:  slog.LevelInfo,
	LogWarn:  slog.LevelWarn,
	LogError: slog.LevelError,
}

func (l LogLevel) String() string { return string(l) }

// Parses a log level as Enum.Parse does.
func (l *LogLevel) UnmarshalText(text []byte) (err error) {
	*l, err = LogLevels.Parse(text)
	return err
}

// Returns l, or DefaultLogLevel when l is empty.
func (l LogLevel) OrDefault() LogLevel {
	if l == "" {
		return DefaultLogLevel
	}
	return l
}

// Returns l.OrDefault() as a slog level.
func (l LogLevel) Level() slog.Level { return slogLevels[l.OrDefault()] }

// The encoding of log records on stderr.
type LogFormat string

const (
	// Text for people to read, one line per record.
	LogText LogFormat = "text"
	// One JSON object per record.
	LogJSON LogFormat = "json"
)

// The valid log formats.
var LogFormats = NewEnum("log format", LogText, LogJSON)

func (f LogFormat) String() string { return string(f) }

// Parses a log format as Enum.Parse does.
func (f *LogFormat) UnmarshalText(text []byte) (err error) {
	*f, err = LogFormats.Parse(text)
	return err
}

// Returns f, or DefaultLogFormat when f is empty.
func (f LogFormat) OrDefault() LogFormat {
	if f == "" {
		return DefaultLogFormat
	}
	return f
}
