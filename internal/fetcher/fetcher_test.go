package fetcher

import (
	"io"

	"github.com/rs/zerolog"
)

// nopLog is a disabled logger for tests that don't care about log output.
var nopLog = zerolog.New(io.Discard)
