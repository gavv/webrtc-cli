package snd

import (
	"bytes"
	"encoding/binary"
	"time"
)

func durationToSamples(d time.Duration, rate int) int {
	return rate * int(d/time.Millisecond) / 1000
}

func bytesToInt16(b []byte) []int16 {
	if len(b)%2 != 0 {
		panic("bad byte slice size")
	}

	ret := make([]int16, len(b)/2)

	rd := bytes.NewReader(b)

	err := binary.Read(rd, binary.LittleEndian, ret)
	if err != nil {
		panic(err)
	}

	return ret
}

func int16ToBytes(i []int16) []byte {
	var b bytes.Buffer
	b.Grow(len(i) * 2)

	err := binary.Write(&b, binary.LittleEndian, i)
	if err != nil {
		panic(err)
	}

	return b.Bytes()
}
