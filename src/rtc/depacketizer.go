package rtc

import (
	"fmt"
	"os"

	"github.com/pion/rtp"
	"golang.org/x/time/rate"
	"gopkg.in/gavv/opus.v2"
)

type depacketizer struct {
	decoder *opus.Decoder

	lastPacket    *rtp.Packet
	lastTimestamp uint32

	logLim *rate.Limiter

	enableFEC bool

	rate     int
	channels int

	nFEC    int
	nPLC int
}

func newDepacketizer(
	decoder *opus.Decoder, enableFEC bool, sampleRate int, channels int, debug bool,
) *depacketizer {
	d := &depacketizer{
		decoder:   decoder,
		enableFEC: enableFEC,
		rate:      sampleRate,
		channels:  channels,
	}

	if debug {
		d.logLim = rate.NewLimiter(rate.Limit(0.5), 1)
	} else {
		d.logLim = rate.NewLimiter(rate.Limit(0.01), 1)
	}

	return d
}

func (d *depacketizer) getSamples(newPacket *rtp.Packet) ([]int16, error) {
	buf, err := d.decodeSamples(newPacket)
	if err != nil {
		return nil, err
	}

	if d.lastPacket == nil {
		d.lastTimestamp = newPacket.Timestamp
	}
	d.lastTimestamp += uint32(len(buf) / d.channels)
	d.lastPacket = newPacket

	if len(buf) == 0 {
		return nil, nil
	}

	return buf, nil
}

func (d *depacketizer) decodeSamples(newPacket *rtp.Packet) ([]int16, error) {
	missing, err := d.decodeMissingSamples(newPacket)
	if err != nil {
		return nil, fmt.Errorf("can't decode missing samples: %s", err.Error())
	}

	current, err := d.decodeNewSamples(newPacket)
	if err != nil {
		return nil, fmt.Errorf("can't decode opus frame: %s", err.Error())
	}

	if current == nil {
		return missing, nil
	}

	if missing == nil {
		return current, nil
	}

	return append(missing, current...), nil
}

func (d *depacketizer) decodeNewSamples(newPacket *rtp.Packet) ([]int16, error) {
	if len(newPacket.Payload) == 0 {
		return nil, nil
	}

	pcm := make([]int16, d.channels*(d.rate*maxFrameMs/1000))

	pcmSamples, err := d.decoder.Decode(newPacket.Payload, pcm)
	if err != nil {
		return nil, err
	}

	pcm = pcm[:pcmSamples*d.channels]

	timestampDiff := 0
	if d.lastPacket != nil {
		timestampDiff = int(int32(newPacket.Timestamp - d.lastTimestamp))
	}

	// new packet is completely after previous
	if timestampDiff >= 0 {
		return pcm, nil
	}

	// new packet is completely covered by previous packets
	if -timestampDiff >= pcmSamples {
		return nil, nil
	}

	// new packet partially overlaps with previous packets
	return pcm[-timestampDiff*d.channels:], nil
}

func (d *depacketizer) decodeMissingSamples(newPacket *rtp.Packet) ([]int16, error) {
	// this is the very first packet
	if d.lastPacket == nil {
		return nil, nil
	}

	// how much samples are missing between the previous and new packet
	missingSamples := int(int32(newPacket.Timestamp-d.lastTimestamp)) * d.channels
	if missingSamples <= 0 {
		return nil, nil
	}

	// get exact size of the last packet, as required by DecodeFEC
	lastPacketLen, err := d.decoder.LastPacketDuration()
	if err != nil || lastPacketLen == 0 {
		return nil, err
	}
	lastPacketLen *= d.channels

	// fill unrecoverable samples
	var left []int16
	if missingSamples > lastPacketLen {
		left = d.decodePLC(missingSamples - lastPacketLen)
	}

	// try to fill recoverable samples (last packet) using FEC
	right := d.decodeFEC(newPacket, lastPacketLen)

	// fallback to PLC if FEC is disabled or failed
	if len(right) == 0 {
		if missingSamples > lastPacketLen {
			right = d.decodePLC(lastPacketLen)
		} else {
			right = d.decodePLC(missingSamples)
		}
	}

	// trim recovered samples if necessary
	if len(right) > missingSamples {
		right = right[len(right)-missingSamples:]
	}

	if d.logLim.Allow() {
		fmt.Fprintf(os.Stderr, "Recovered %d samples using FEC and %d samples using PLC\n",
			d.nFEC, d.nPLC)
		d.nFEC, d.nPLC = 0, 0
	}

	// concatenate ranges
	if len(left) == 0 {
		return right, nil
	} else {
		return append(left, right...), nil
	}
}

func (d *depacketizer) decodeFEC(newPacket *rtp.Packet, lastPacketLen int) []int16 {
	if !d.enableFEC {
		return nil
	}

	pcm := make([]int16, lastPacketLen)
	if err := d.decoder.DecodeFEC(newPacket.Payload, pcm); err != nil {
		return nil
	}

	d.nFEC += len(pcm)

	return pcm
}

func (d *depacketizer) decodePLC(numSamples int) []int16 {
	pcm := make([]int16, numSamples)

	_ = d.decoder.DecodePLC(pcm)

	d.nPLC += len(pcm)

	return pcm
}
