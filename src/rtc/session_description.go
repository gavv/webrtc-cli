package rtc

import (
	"net"
	"strings"

	"github.com/pion/sdp/v3"
	"github.com/pion/webrtc/v3"
)

func postprocessSDP(desc *webrtc.SessionDescription, overrideIP string) error {
	if overrideIP != "" {
		parsedDesc := sdp.SessionDescription{}

		if err := parsedDesc.Unmarshal([]byte(desc.SDP)); err != nil {
			return err
		}

		overrideCandidatesIP(&parsedDesc, overrideIP)

		b, err := parsedDesc.Marshal()
		if err != nil {
			return err
		}

		desc.SDP = string(b)
	}

	return nil
}

func overrideCandidatesIP(desc *sdp.SessionDescription, overrideIP string) {
	for _, md := range desc.MediaDescriptions {
		for n := range md.Attributes {
			at := &md.Attributes[n]

			if at.Key != "candidate" {
				continue
			}

			const ipIndex = 4

			fields := strings.Split(at.Value, " ")
			if len(fields) < ipIndex+1 {
				continue
			}

			if net.ParseIP(fields[ipIndex]) == nil {
				continue
			}

			fields[ipIndex] = overrideIP

			at.Value = strings.Join(fields, " ")
		}
	}
}
