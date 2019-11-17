package snd

import (
	"fmt"
	"runtime"

	"github.com/mesilliac/pulse-simple"
)

type PulsePlayer struct {
	initCh   chan error
	dataCh   chan []int16
	errCh    chan error
	cancelCh chan struct{}
	doneCh   chan struct{}
}

func NewPulsePlayer(params Params) (*PulsePlayer, error) {
	p := &PulsePlayer{
		initCh:   make(chan error, 1),
		dataCh:   make(chan []int16, 0),
		errCh:    make(chan error, 1),
		cancelCh: make(chan struct{}),
		doneCh:   make(chan struct{}),
	}

	go func() {
		runtime.LockOSThread()
		p.runPlayback(params)
	}()

	if err := <-p.initCh; err != nil {
		<-p.doneCh
		return nil, err
	}

	return p, nil
}

func (p *PulsePlayer) Batches() chan<- []int16 {
	return p.dataCh
}

func (p *PulsePlayer) Errors() <-chan error {
	return p.errCh
}

func (p *PulsePlayer) Stopped() <-chan struct{} {
	return p.cancelCh
}

func (p *PulsePlayer) Stop() {
	close(p.cancelCh)
	<-p.doneCh
}

func (p *PulsePlayer) runPlayback(params Params) {
	defer func() {
		close(p.doneCh)
		close(p.errCh)
	}()

	sample_spec := pulse.SampleSpec{
		Format:   pulse.SAMPLE_S16LE,
		Rate:     uint32(params.Rate),
		Channels: uint8(params.Channels),
	}

	stream, err := pulse.NewStream(
		"", "webrtc-cli", pulse.STREAM_PLAYBACK, params.DeviceOrFile, "playback",
		&sample_spec, nil, nil)

	if err != nil {
		p.initCh <- fmt.Errorf("can't open pulseaudio stream: %s", err.Error())
		return
	}

	close(p.initCh)

	defer stream.Free()
	defer stream.Drain()

	for {
		var data []int16

		select {
		case data = <-p.dataCh:
		case <-p.cancelCh:
			return
		}

		if len(data) == 0 {
			continue
		}

		b := int16ToBytes(data)

		n, err := stream.Write(b)
		if err != nil {
			p.errCh <- fmt.Errorf("can't write to pulseaudio stream: %s", err.Error())
			return
		}

		if n != len(b) {
			panic("unexpected write size from pulseaudio")
		}
	}
}
