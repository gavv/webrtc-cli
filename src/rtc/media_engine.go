package rtc

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/pion/sdp/v3"
	"github.com/pion/webrtc/v3"
)

func newMediaEngine(params Params) *webrtc.MediaEngine {
	mediaEngine := &webrtc.MediaEngine{}

	err := mediaEngine.RegisterCodec(
		webrtc.RTPCodecParameters{
			RTPCodecCapability: webrtc.RTPCodecCapability{
				MimeType:     "audio/opus",
				ClockRate:    uint32(params.Rate),
				Channels:     uint16(params.Channels),
				SDPFmtpLine:  "",
				RTCPFeedback: nil,
			},
			PayloadType: 111,
		},
		webrtc.RTPCodecTypeAudio)

	if err != nil {
		panic(err)
	}

	return mediaEngine
}

func newMediaEngineFromOffer(params Params, offer *webrtc.SessionDescription) (
	*webrtc.MediaEngine, bool, error,
) {
	mediaEngine := &webrtc.MediaEngine{}

	opusCodec, err := populateFromSDP(mediaEngine, offer)
	if err != nil {
		return nil, false, err
	}

	if opusCodec == nil {
		return nil, false, fmt.Errorf("opus not offered")
	}
	if int(opusCodec.ClockRate) != params.Rate {
		return nil, false, fmt.Errorf("want %d rate, offered %d rate",
			params.Rate, opusCodec.ClockRate)
	}
	if int(opusCodec.Channels) != params.Channels {
		return nil, false, fmt.Errorf("want %d channels, offered %d channels",
			params.Channels, opusCodec.Channels)
	}

	enableFEC := strings.Contains(opusCodec.SDPFmtpLine, "useinbandfec=1")

	return mediaEngine, enableFEC, nil
}

// based on webrtc.MediaEngine.PopulateFromSDP
func populateFromSDP(m *webrtc.MediaEngine, offer *webrtc.SessionDescription) (
	*webrtc.RTPCodecParameters, error,
) {
	var codec *webrtc.RTPCodecParameters

	parsedOffer := sdp.SessionDescription{}

	if err := parsedOffer.Unmarshal([]byte(offer.SDP)); err != nil {
		return nil, err
	}

	for _, md := range parsedOffer.MediaDescriptions {
		if md.MediaName.Media != "audio" {
			continue
		}

		for _, format := range md.MediaName.Formats {
			pt, err := strconv.Atoi(format)
			if err != nil {
				return nil, fmt.Errorf("format parse error")
			}

			if pt < 0 || pt > 255 {
				return nil, fmt.Errorf("payload type out of range: %d", pt)
			}

			payloadType := uint8(pt)
			payloadCodec, err := parsedOffer.GetCodecForPayloadType(payloadType)
			if err != nil {
				return nil, fmt.Errorf("could not find codec for payload type %d", payloadType)
			}

			channels := uint16(0)
			if val, err := strconv.Atoi(payloadCodec.EncodingParameters); err == nil {
				channels = uint16(val)
			}

			feedback := []webrtc.RTCPFeedback{}
			for _, raw := range payloadCodec.RTCPFeedback {
				split := strings.Split(raw, " ")
				entry := webrtc.RTCPFeedback{Type: split[0]}
				if len(split) == 2 {
					entry.Parameter = split[1]
				}
				feedback = append(feedback, entry)
			}

			switch payloadCodec.Name {
			case "opus":
				codec = &webrtc.RTPCodecParameters{
					RTPCodecCapability: webrtc.RTPCodecCapability{
						MimeType:     md.MediaName.Media + "/" + payloadCodec.Name,
						ClockRate:    payloadCodec.ClockRate,
						Channels:     channels,
						SDPFmtpLine:  payloadCodec.Fmtp,
						RTCPFeedback: feedback,
					},
					PayloadType: webrtc.PayloadType(pt),
				}
			default:
				continue
			}

			if err := m.RegisterCodec(*codec, webrtc.RTPCodecTypeAudio); err != nil {
				return nil, err
			}
		}
	}

	return codec, nil
}
