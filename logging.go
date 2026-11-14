package autoscan

import (
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// ParseVerbosity returns the log level of a verbosity option. It returns
// false if verbosity is empty or is not a valid level. The caller then
// keeps the global log level.
func ParseVerbosity(verbosity string) (zerolog.Level, bool) {
	if verbosity == "" {
		return zerolog.NoLevel, false
	}

	level, err := zerolog.ParseLevel(verbosity)
	if err != nil {
		return zerolog.NoLevel, false
	}

	return level, true
}

func GetLogger(verbosity string) zerolog.Logger {
	level, ok := ParseVerbosity(verbosity)
	if !ok {
		return log.Logger
	}

	return log.Level(level)
}
