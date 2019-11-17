package snd

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/youpy/go-wav"
	"golang.org/x/time/rate"
)

type WavReader struct {
	fp *os.File
	rd *wav.Reader

	batchCh  chan Batch
	cancelCh chan struct{}
	doneCh   chan struct{}
}

func NewWavReader(params Params) (*WavReader, error) {
	fp, err := os.Open(params.DeviceOrFile)
	if err != nil {
		return nil, fmt.Errorf("can't open wav file: %s", err.Error())
	}

	rd := wav.NewReader(fp)

	format, err := rd.Format()
	if err != nil {
		fp.Close()
		return nil, fmt.Errorf("can't read wav file header: %s", err.Error())
	}

	if int(format.SampleRate) != params.Rate {
		fp.Close()
		return nil, fmt.Errorf("bad wav file: need rate %d, got rate %d",
			params.Rate, format.SampleRate)
	}

	if int(format.NumChannels) != params.Channels {
		fp.Close()
		return nil, fmt.Errorf("bad wav file: need %d channels, got %d channels",
			params.Channels, format.NumChannels)
	}

	w := &WavReader{
		fp:       fp,
		rd:       rd,
		batchCh:  make(chan Batch, 64),
		cancelCh: make(chan struct{}),
		doneCh:   make(chan struct{}),
	}

	go w.runReading(params)

	return w, nil
}

func (w *WavReader) Batches() <-chan Batch {
	return w.batchCh
}

func (w *WavReader) Stop() {
	close(w.cancelCh)
	<-w.doneCh
}

func (w *WavReader) runReading(params Params) {
	defer close(w.doneCh)
	defer close(w.batchCh)

	defer w.fp.Close()

	samplesPerFramePerChan := durationToSamples(params.FrameLength, params.Rate)

	limiter := rate.NewLimiter(rate.Limit(params.Rate), samplesPerFramePerChan)

	for {
		select {
		case <-w.cancelCh:
			return
		default:
		}

		samples, err := w.rd.ReadSamples(uint32(samplesPerFramePerChan))
		if err == io.EOF {
			return
		}
		if err != nil {
			w.batchCh <- Batch{
				Err: fmt.Errorf("can't read from wav file: %s", err.Error()),
			}
			return
		}

		if len(samples) == 0 || len(samples) > samplesPerFramePerChan {
			panic("unexpected read size from wav file")
		}

		data := make([]int16, samplesPerFramePerChan*params.Channels)
		n := 0

		for _, sample := range samples {
			for i := 0; i < params.Channels; i++ {
				data[n] = int16(w.rd.IntValue(sample, uint(i)))
				n++
			}
		}

		limiter.WaitN(context.TODO(), samplesPerFramePerChan)

		w.batchCh <- Batch{
			Data: data,
		}
	}
}
