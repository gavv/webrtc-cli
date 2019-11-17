package dsp

import (
	"time"
)

func durationToSamples(d time.Duration, rate int) int {
	return rate * int(d/time.Millisecond) / 1000
}
