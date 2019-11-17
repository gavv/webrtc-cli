package snd

import (
	"time"
)

type Params struct {
	DeviceOrFile string
	Rate         int
	Channels     int
	FrameLength  time.Duration
}

type Batch struct {
	Data []int16
	Err  error
}

type Reader interface {
	Batches() <-chan Batch
	Stop()
}

func NewReader(params Params) (Reader, error) {
	if isWavFile(params.DeviceOrFile) {
		return NewWavReader(params)
	}
	return NewPulseRecorder(params)
}
