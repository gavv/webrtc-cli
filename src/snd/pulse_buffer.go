package snd

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

func SetPulseBufferSize(d time.Duration) error {
	msec := int(d / time.Millisecond)

	err := os.Setenv("PULSE_LATENCY_MSEC", strconv.Itoa(msec))
	if err != nil {
		return fmt.Errorf("can't set PULSE_LATENCY_MSEC: %s", err.Error())
	}

	return nil
}
