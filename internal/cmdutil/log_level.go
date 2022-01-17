package cmdutil

import (
	"fmt"
	"strings"

	"github.com/go-kit/log/level"
)

var defaultLogLevel = LogLevel{
	value:  level.InfoValue(),
	option: level.AllowInfo(),
}

// LogLevel implements flag.Value and can be used to set the logging level from a flag. The zero value is ready for use.
type LogLevel struct {
	value  level.Value
	option level.Option
}

// String implements flag.Value.
func (l LogLevel) String() string {
	if l.value == nil {
		return defaultLogLevel.String()
	}
	return l.value.String()
}

// Set implements flag.Value.
func (l *LogLevel) Set(in string) error {
	switch strings.ToLower(in) {
	case "error":
		l.value = level.ErrorValue()
		l.option = level.AllowError()
	case "warn":
		l.value = level.WarnValue()
		l.option = level.AllowWarn()
	case "info":
		l.value = level.InfoValue()
		l.option = level.AllowInfo()
	case "debug":
		l.value = level.DebugValue()
		l.option = level.AllowDebug()
	default:
		return fmt.Errorf("unknown log level %q, valid options error, warn, info, debug", in)
	}
	return nil
}

// Return l as a FilterOption that can be used with level.NewFilter.
func (l LogLevel) FilterOption() level.Option {
	if l.option == nil {
		return defaultLogLevel.option
	}
	return l.option
}
