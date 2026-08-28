package config

import "log/slog"

// levels are the log-level setting in the vocabulary slog uses. The table is
// here rather than in a switch so that the names Validate accepts and the
// levels the process runs at cannot drift apart.
var levels = map[string]slog.Level{
	"debug": slog.LevelDebug,
	"info":  slog.LevelInfo,
	"warn":  slog.LevelWarn,
	"error": slog.LevelError,
}

// LogLevelValue is the log-level setting in the vocabulary of the code that
// uses it: the lowest level the process keeps. The string spelling stops here,
// as it does for the call timeout and the two policies.
//
// Load has already validated it, so a Config that came from Load resolves to a
// level it named. One built by hand and never validated resolves to info, which
// is the default the string layer starts from.
func (c Config) LogLevelValue() slog.Level {
	if level, named := levels[c.LogLevel]; named {
		return level
	}
	return slog.LevelInfo
}
