package snd

import (
	"fmt"
	"runtime"

	"github.com/mesilliac/pulse-simple"
)

type PulseRecorder struct {
	initCh   chan error
	batchCh  chan Batch
	cancelCh chan struct{}
	doneCh   chan struct{}
}

func NewPulseRecorder(params Params) (*PulseRecorder, error) {
	p := &PulseRecorder{
		initCh:   make(chan error, 1),
		batchCh:  make(chan Batch, 1),
		cancelCh: make(chan struct{}),
		doneCh:   make(chan struct{}),
	}

	go func() {
		runtime.LockOSThread()
		p.runRecording(params)
	}()

	if err := <-p.initCh; err != nil {
		<-p.doneCh
		return nil, err
	}

	return p, nil
}

func (p *PulseRecorder) Batches() <-chan Batch {
	return p.batchCh
}

func (p *PulseRecorder) Stop() {
	close(p.cancelCh)

	// there is no way to interrupt pa_simple_read() blocked on suspended device
	// so we can't wait its termination
}

func (p *PulseRecorder) runRecording(params Params) {
	defer func() {
		close(p.doneCh)
		close(p.batchCh)
	}()

	sample_spec := pulse.SampleSpec{
		Format:   pulse.SAMPLE_S16LE,
		Rate:     uint32(params.Rate),
		Channels: uint8(params.Channels),
	}

	stream, err := pulse.NewStream(
		"", "webrtc-cli", pulse.STREAM_RECORD, params.DeviceOrFile, "webrtc-cli-record",
		&sample_spec, nil, nil)

	if err != nil {
		p.initCh <- fmt.Errorf("can't open pulseaudio record stream: %s", err.Error())
		return
	}

	close(p.initCh)

	defer stream.Free()

	frameBytes := durationToSamples(params.FrameLength, params.Rate) *
		int(sample_spec.FrameSize())

	for {
		select {
		case <-p.cancelCh:
			return
		default:
		}

		data := make([]byte, frameBytes)

		n, err := stream.Read(data)
		if err != nil {
			p.batchCh <- Batch{
				Err: fmt.Errorf("can't read from pulseaudio record stream: %s", err.Error()),
			}
			return
		}

		if n == 0 || n > len(data) {
			panic("unexpected read size from pulseaudio")
		}

		batch := Batch{
			Data: bytesToInt16(data),
		}

		select {
		case p.batchCh <- batch:
		case <-p.cancelCh:
			return
		}
	}
}
