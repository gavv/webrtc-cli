package rtc

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"time"

	"github.com/pion/rtp"
	"github.com/pion/webrtc/v3"
	"github.com/pion/webrtc/v3/pkg/media"
	"gopkg.in/gavv/opus.v2"
)

type State string

func (s State) String() string {
	return string(s)
}

func (s State) IsConnected() bool {
	return s == State(webrtc.ICEConnectionStateConnected.String())
}

type Mode int

const (
	ModeVoIP     = Mode(opus.AppVoIP)
	ModeAudio    = Mode(opus.AppAudio)
	ModeLowdelay = Mode(opus.AppRestrictedLowdelay)
)

type Params struct {
	IceURL string

	MinPort    uint16
	MaxPort    uint16
	OverrideIP string

	OfferSDP string

	EnableWrite bool
	EnableRead  bool

	Rate     int
	Channels int

	Mode        Mode
	Complexity  int
	LossPercent int

	SimulateLossPercent int

	Debug bool
}

type Peer struct {
	conn *webrtc.PeerConnection

	offer  *webrtc.SessionDescription
	answer *webrtc.SessionDescription

	localTrack  *webrtc.TrackLocalStaticSample
	remoteTrack *webrtc.TrackRemote

	encoder      *opus.Encoder
	decoder      *opus.Decoder
	depacketizer *depacketizer

	rate             int
	channels         int
	simulateLossPerc int

	remoteTrackCh chan struct{}
	connCh        chan State
	closingCh     chan struct{}
	closedCh      chan struct{}
}

func NewPeer(params Params) (*Peer, error) {
	p := &Peer{
		rate:             params.Rate,
		channels:         params.Channels,
		simulateLossPerc: params.SimulateLossPercent,
		remoteTrackCh:    make(chan struct{}),
		connCh:           make(chan State, 128),
		closingCh:        make(chan struct{}),
		closedCh:         make(chan struct{}),
	}

	var mediaEngine *webrtc.MediaEngine
	var err error
	var enableFEC bool

	if params.OfferSDP == "" {
		mediaEngine = newMediaEngine(params)
		enableFEC = true
	} else {
		p.offer = &webrtc.SessionDescription{
			Type: webrtc.SDPTypeOffer,
			SDP:  params.OfferSDP,
		}
		mediaEngine, enableFEC, err = newMediaEngineFromOffer(params, p.offer)
		if err != nil {
			return nil, fmt.Errorf("can't create media engine from offer: %s", err.Error())
		}
	}

	settingEngine := webrtc.SettingEngine{}
	if params.MinPort != 0 || params.MaxPort != 0 {
		fmt.Fprintf(os.Stderr, "Using UDP port range [%d; %d]\n",
			params.MinPort, params.MaxPort)
		settingEngine.SetEphemeralUDPPortRange(params.MinPort, params.MaxPort)
	}

	api := webrtc.NewAPI(webrtc.WithMediaEngine(mediaEngine),
		webrtc.WithSettingEngine(settingEngine))

	p.conn, err = api.NewPeerConnection(webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{
				URLs: []string{params.IceURL},
			},
		},
	})
	if err != nil {
		return nil, err
	}

	if params.EnableWrite {
		p.localTrack, err = webrtc.NewTrackLocalStaticSample(
			webrtc.RTPCodecCapability{
				MimeType: "audio/opus",
			},
			"audio",
			"webrtc-cli")
		if err != nil {
			return nil, fmt.Errorf("can't create local track: %s", err.Error())
		}

		if _, err = p.conn.AddTrack(p.localTrack); err != nil {
			return nil, fmt.Errorf("can't add local track to connection: %s", err.Error())
		}

		p.encoder, err = opus.NewEncoder(
			params.Rate, params.Channels, opus.Application(params.Mode))
		if err != nil {
			return nil, fmt.Errorf("can't create opus encoder: %s", err.Error())
		}

		if err := p.encoder.SetComplexity(params.Complexity); err != nil {
			return nil, fmt.Errorf("can't set complexity: %s", err.Error())
		}

		if enableFEC {
			fmt.Fprintln(os.Stderr, "Enabling in-band FEC")

			if err := p.encoder.SetPacketLossPerc(params.LossPercent); err != nil {
				return nil, fmt.Errorf("can't set packet loss percent: %s", err.Error())
			}
		}
		if err := p.encoder.SetInBandFEC(enableFEC); err != nil {
			return nil, fmt.Errorf("can't set inband fec: %s", err.Error())
		}
	}

	if params.EnableRead {
		if _, err = p.conn.AddTransceiverFromKind(webrtc.RTPCodecTypeAudio); err != nil {
			return nil, fmt.Errorf("can't add transceiver: %s", err.Error())
		}

		p.conn.OnTrack(func(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
			if p.remoteTrack == nil {
				fmt.Fprintln(os.Stderr, "Accepting remote track")
				p.remoteTrack = track
				close(p.remoteTrackCh)
			} else {
				fmt.Fprintln(os.Stderr, "Ignoring remote track")
			}
		})

		p.decoder, err = opus.NewDecoder(params.Rate, params.Channels)
		if err != nil {
			return nil, fmt.Errorf("can't create opus decoder: %s", err.Error())
		}

		p.depacketizer = newDepacketizer(
			p.decoder, enableFEC, params.Rate, params.Channels, params.Debug)
	}

	p.conn.OnICEConnectionStateChange(
		func(connState webrtc.ICEConnectionState) {
			select {
			case p.connCh <- State(connState.String()):
			default:
				select {
				case p.connCh <- State(connState.String()):
				case <-p.closingCh:
				}
			}

			if connState == webrtc.ICEConnectionStateClosed {
				close(p.connCh)
				close(p.closedCh)
			}
		})

	if p.offer == nil {
		offer, err := p.conn.CreateOffer(nil)
		if err != nil {
			return nil, fmt.Errorf("can't create offer: %s", err.Error())
		}

		p.offer = &offer

		if err := p.conn.SetLocalDescription(*p.offer); err != nil {
			return nil, fmt.Errorf("can't set sdp offer: %s", err.Error())
		}

		if err := postprocessSDP(p.offer, params.OverrideIP); err != nil {
			return nil, fmt.Errorf("can't process sdp offer: %s", err.Error())
		}
	} else {
		if err := p.conn.SetRemoteDescription(*p.offer); err != nil {
			return nil, fmt.Errorf("can't set sdp offer: %s", err.Error())
		}

		answer, err := p.conn.CreateAnswer(nil)
		if err != nil {
			return nil, fmt.Errorf("can't create sdp answer: %s", err.Error())
		}

		p.answer = &answer

		if err := p.conn.SetLocalDescription(*p.answer); err != nil {
			return nil, fmt.Errorf("can't set sdp answer: %s", err.Error())
		}

		if err := postprocessSDP(p.answer, params.OverrideIP); err != nil {
			return nil, fmt.Errorf("can't process sdp answer: %s", err.Error())
		}
	}

	return p, nil
}

func (p *Peer) Close() error {
	close(p.closingCh)

	if err := p.conn.Close(); err != nil {
		return err
	}

	<-p.closedCh
	return nil
}

func (p *Peer) State() <-chan State {
	return p.connCh
}

func (p *Peer) GetOffer() string {
	if p.offer == nil {
		return ""
	}
	return strings.TrimSpace(p.offer.SDP)
}

func (p *Peer) GetAnswer() string {
	if p.answer == nil {
		return ""
	}
	return strings.TrimSpace(p.answer.SDP)
}

func (p *Peer) SetAnswer(s string) error {
	answer := webrtc.SessionDescription{
		Type: webrtc.SDPTypeAnswer,
		SDP:  string(s),
	}
	if err := p.conn.SetRemoteDescription(answer); err != nil {
		return fmt.Errorf("can't set sdp answer: %s", err.Error())
	}
	p.answer = &answer
	return nil
}

func (p *Peer) Write(pcm []int16) error {
	if p.localTrack == nil {
		panic("writing not enabled for peer")
	}

	b := make([]byte, maxFrameBytes)

	n, err := p.encoder.Encode(pcm, b)
	if err != nil {
		return fmt.Errorf("can't encode opus frame: %s", err.Error())
	}

	err = p.localTrack.WriteSample(media.Sample{
		Data: b[:n],
		Duration: time.Duration(
			int64(len(pcm)/p.channels) * int64(time.Second) / int64(p.rate)),
	})
	if err != nil {
		return fmt.Errorf("can't send frame: %s", err.Error())
	}

	return nil
}

func (p *Peer) Read() ([]int16, error) {
	for {
		newPacket, err := p.getPacket()
		if err != nil {
			return nil, err
		}

		if rand.Intn(100) < p.simulateLossPerc {
			continue
		}

		buf, err := p.depacketizer.getSamples(newPacket)
		if err != nil {
			return nil, err
		}

		if len(buf) == 0 {
			continue
		}

		return buf, nil
	}
}

func (p *Peer) getPacket() (*rtp.Packet, error) {
	select {
	case <-p.remoteTrackCh:
	case <-p.closingCh:
		return nil, errors.New("peer is closed")
	}

	pkt, _, err := p.remoteTrack.ReadRTP()
	if err != nil {
		return nil, fmt.Errorf("can't read RTP packet: %s", err.Error())
	}

	return pkt, nil
}
