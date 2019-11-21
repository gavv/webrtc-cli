package dsp

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

type JitterBufParams struct {
	Rate         int
	Channels     int
	FrameLength  time.Duration
	BufferLength time.Duration
	MaxDrift     time.Duration
	Debug        bool
}

type JitterBuf struct {
	mu sync.Mutex

	starting bool
	closed   bool

	rpos uint64
	wpos uint64

	buf []int16

	frameSize  int
	targetSize int
	minSize    int
	maxSize    int

	logDrop  *rate.Limiter
	logZero  *rate.Limiter
	logReset *rate.Limiter
	logSize  *rate.Limiter

	nDropped int
	nZeros   int
	nResets  int
}

func NewJitterBuf(p JitterBufParams) (*JitterBuf, error) {
	frameSize := durationToSamples(p.FrameLength, p.Rate) * p.Channels

	bufferSize := durationToSamples(p.BufferLength, p.Rate) * p.Channels
	bufferDrift := durationToSamples(p.MaxDrift, p.Rate) * p.Channels

	freq := 0.01
	if p.Debug {
		freq = 0.5
	}

	j := &JitterBuf{
		starting:   true,
		frameSize:  frameSize,
		targetSize: bufferSize,
		minSize:    bufferSize - bufferDrift,
		maxSize:    bufferSize + bufferDrift,
		logDrop:    rate.NewLimiter(rate.Limit(freq), 1),
		logZero:    rate.NewLimiter(rate.Limit(freq), 1),
		logReset:   rate.NewLimiter(rate.Limit(freq), 1),
		logSize:    rate.NewLimiter(rate.Limit(freq), 1),
	}

	return j, nil
}

func (j *JitterBuf) Stop() {
	j.mu.Lock()
	defer j.mu.Unlock()

	j.closed = true
}

func (j *JitterBuf) Write(buf []int16) {
	j.mu.Lock()
	defer j.mu.Unlock()

	j.writeFrame(buf)

	j.validateBuffer()
}

func (j *JitterBuf) writeFrame(buf []int16) {
	if j.closed {
		return
	}

	bufLen := len(buf)

	bsize := j.bufferSize()
	if bsize < 0 {
		shiftLen := -bsize
		if shiftLen > bufLen {
			shiftLen = bufLen
		}

		buf = buf[shiftLen:]

		j.nDropped += shiftLen
		if j.logDrop.Allow() {
			fmt.Fprintf(os.Stderr, "Dropped %d outdated samples\n", j.nDropped)
			j.nDropped = 0
		}
	}

	if len(buf) != 0 {
		j.buf = append(j.buf, buf...)
	}

	j.wpos += uint64(bufLen)
}

func (j *JitterBuf) Read() ([]int16, error) {
	j.mu.Lock()
	defer j.mu.Unlock()

	return j.readFrame()
}

func (j *JitterBuf) readFrame() ([]int16, error) {
	if j.closed {
		return nil, errors.New("jitter buffer is closed")
	}

	if !j.starting {
		bsize := j.bufferSize()

		if bsize <= j.minSize || bsize >= j.maxSize {
			j.resetBuffer()
			j.validateBuffer()
			j.starting = true
		}
	}

	if j.starting {
		bsize := j.bufferSize()

		if bsize < j.targetSize {
			return make([]int16, j.frameSize), nil
		}

		if bsize > j.targetSize+j.frameSize {
			j.trimBuffer(j.targetSize + j.frameSize)
		}
	}

	j.validateBuffer()

	j.starting = false

	frame := j.shiftFrame()

	return frame, nil
}

func (j *JitterBuf) shiftFrame() []int16 {
	if j.logSize.Allow() {
		fmt.Fprintf(os.Stderr, "Jitter buffer size is %d, target size is %d\n",
			j.bufferSize(), j.targetSize)
	}

	j.rpos += uint64(j.frameSize)

	if len(j.buf) >= j.frameSize {
		frame := j.buf[:j.frameSize]
		j.buf = j.buf[j.frameSize:]
		return frame
	}

	j.nZeros += j.frameSize - len(j.buf)
	if j.logZero.Allow() {
		fmt.Fprintf(os.Stderr,
			"Inserted zeros instead of %d delayed samples\n", j.nZeros)
		j.nZeros = 0
	}

	frame := make([]int16, j.frameSize)
	copy(frame, j.buf)
	j.buf = nil
	return frame
}

func (j *JitterBuf) resetBuffer() {
	j.nResets++

	if j.logReset.Allow() {
		fmt.Fprintf(os.Stderr, "Resetting buffer %d times, buffer size is %d\n",
			j.nResets, j.bufferSize())
		j.nResets = 0
	}

	j.rpos = 0
	j.wpos = uint64(len(j.buf))
}

func (j *JitterBuf) trimBuffer(maxSize int) {
	shiftLen := j.bufferSize() - maxSize

	if shiftLen >= len(j.buf) {
		j.buf = nil
	} else {
		j.buf = j.buf[shiftLen:]
	}

	j.rpos += uint64(shiftLen)
}

func (j *JitterBuf) validateBuffer() {
	bsize := j.bufferSize()

	if bsize >= 0 {
		if len(j.buf) != bsize {
			panic(fmt.Sprintf("Corrupted buffer: buffer size is %d, buffer size is %d",
				bsize, len(j.buf)))
		}
	} else {
		if len(j.buf) != 0 {
			panic(fmt.Sprintf("Corrupted buffer: buffer size is %d, buffer size is %d",
				bsize, len(j.buf)))
		}
	}
}

func (j *JitterBuf) bufferSize() int {
	return int(int64(j.wpos - j.rpos))
}
