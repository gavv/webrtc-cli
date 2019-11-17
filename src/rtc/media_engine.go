package rtc

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/pion/sdp/v2"
	"github.com/pion/webrtc/v2"
)

func newMediaEngine(params Params) *webrtc.MediaEngine {
	mediaEngine := &webrtc.MediaEngine{}

	mediaEngine.RegisterCodec(
		webrtc.NewRTPOpusCodec(webrtc.DefaultPayloadTypeOpus, uint32(params.Rate)))

	return mediaEngine
}

func newMediaEngineFromOffer(params Params, offer *webrtc.SessionDescription) (
	*webrtc.MediaEngine, bool, error,
) {
	mediaEngine := &webrtc.MediaEngine{}

	if err := populateFromSDP(mediaEngine, offer); err != nil {
		return nil, false, err
	}

	var codec *webrtc.RTPCodec
	for _, c := range mediaEngine.GetCodecsByKind(webrtc.RTPCodecTypeAudio) {
		if c.Name == webrtc.Opus {
			codec = c
			break
		}
	}

	if codec == nil {
		return nil, false, fmt.Errorf("opus not offered")
	}
	if int(codec.ClockRate) != params.Rate {
		return nil, false, fmt.Errorf("want %d rate, offered %d rate",
			params.Rate, codec.ClockRate)
	}
	if int(codec.Channels) != params.Channels {
		return nil, false, fmt.Errorf("want %d channels, offered %d channels",
			params.Channels, codec.Channels)
	}

	enableFEC := strings.Contains(codec.SDPFmtpLine, "useinbandfec=1")

	return mediaEngine, enableFEC, nil
}

// based on webrtc.MediaEngine.PopulateFromSDP
func populateFromSDP(m *webrtc.MediaEngine, offer *webrtc.SessionDescription) error {
	parsedOffer := sdp.SessionDescription{}

	if err := parsedOffer.Unmarshal([]byte(offer.SDP)); err != nil {
		return err
	}

	for _, md := range parsedOffer.MediaDescriptions {
		if md.MediaName.Media != "audio" {
			continue
		}

		for _, format := range md.MediaName.Formats {
			pt, err := strconv.Atoi(format)
			if err != nil {
				return fmt.Errorf("format parse error")
			}

			if pt < 0 || pt > 255 {
				return fmt.Errorf("payload type out of range: %d", pt)
			}

			payloadType := uint8(pt)
			payloadCodec, err := parsedOffer.GetCodecForPayloadType(payloadType)
			if err != nil {
				return fmt.Errorf("could not find codec for payload type %d", payloadType)
			}

			var codec *webrtc.RTPCodec
			switch payloadCodec.Name {
			case webrtc.Opus:
				codec = webrtc.NewRTPOpusCodec(payloadType, payloadCodec.ClockRate)
				codec.SDPFmtpLine = payloadCodec.Fmtp
			default:
				continue
			}

			m.RegisterCodec(codec)
		}
	}
	return nil
}
