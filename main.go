package main

import (
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mattn/go-isatty"
	"github.com/spf13/pflag"

	"github.com/gavv/webrtc-cli/src/dsp"
	"github.com/gavv/webrtc-cli/src/rtc"
	"github.com/gavv/webrtc-cli/src/snd"
)

const (
	delim1 = "--------------------------8<--------------------------"
	delim2 = "-------------------------->8--------------------------"
)

func main() {
	os.Exit(mainWithCode())
}

func mainWithCode() int {
	fset := pflag.NewFlagSet("webrtc-cli", pflag.ContinueOnError)

	offer := fset.Bool("offer", false, "enable offer mode")
	answer := fset.Bool("answer", false, "enable answer mode")

	source := fset.String("source", "", "pulseaudio source or input wav file")
	sink := fset.String("sink", "", "pulseaudio sink")

	timeout := fset.Duration("timeout", 0, "exit if can't connect during timeout")

	stun := fset.String("stun", "stun:stun.l.google.com:19302", "STUN server URL")
	ice := fset.String("ice", "stun:stun.l.google.com:19302", "STUN or TURN server URL")

	_ = fset.MarkDeprecated("stun", "please use --ice instead")

	ports := fset.String("ports", "", "use specific UDP port range (e.g. \"3100:3200\")")
	overrideIP := fset.String("override-ip", "", "override IP address in SDP offer/answer")

	rate := fset.Uint("rate", 48000, "sample rate")
	channels := fset.Uint("chans", 2, "# of channels")

	sourceFrame := fset.Duration("source-frame", 40*time.Millisecond, "source frame size")
	sinkFrame := fset.Duration("sink-frame", 40*time.Millisecond, "sink frame size")
	jitterBuf := fset.Duration("jitter-buf", 120*time.Millisecond, "jitter buffer size")
	pulseBuf := fset.Duration("pulse-buf", 20*time.Millisecond, "pulseaudio buffer size")

	maxDrift := fset.Duration("max-drift", 30*time.Millisecond,
		"maximum jitter buffer drift")

	modeStr := fset.String("mode", "voip", "opus encoder mode: voip|audio|lowdelay")

	complexity := fset.Uint("complexity", 10, "opus encoder complexity")

	lossPerc := fset.Uint("loss-perc", 25,
		"expected packet loss percent, passed to opus encoder")

	simLossPerc := fset.Uint("simulate-loss-perc", 0,
		"simulate given loss percent when receiving packets")

	debug := fset.Bool("debug", false, "enable more logs")

	fset.SortFlags = false

	if err := fset.Parse(os.Args[1:]); err != nil {
		if err == pflag.ErrHelp {
			return 0
		}
		printErr(err)
		return 1
	}

	if *offer == *answer {
		printErrMsg("exactly one of --offer and --answer options should be specified")
		return 1
	}

	var minPort, maxPort uint16
	var err error
	if *ports != "" {
		minPort, maxPort, err = parsePorts(*ports)
		if err != nil {
			printErrMsg("invalid --ports: " + err.Error())
			return 1
		}
	}

	if *rate != 48000 && *rate != 96000 {
		printErrMsg("--rate should be 48000 or 96000")
		return 1
	}

	if *channels != 1 && *channels != 2 {
		printErrMsg("--chans should be 1 or 2")
		return 1
	}

	mode, err := parseMode(*modeStr)
	if err != nil {
		printErrMsg("invalid --mode: " + err.Error())
		return 1
	}

	if *complexity > 10 {
		printErrMsg("invalid --complexity: should be in [0; 10]")
		return 1
	}

	if *lossPerc > 100 {
		printErrMsg("--loss-perc should be in [0; 100]")
		return 1
	}

	if *simLossPerc > 100 {
		printErrMsg("--simulate-loss-perc should be in [0; 100]")
		return 1
	}

	if fset.Changed("loss-perc") && *source == "" {
		printErrMsg("--loss-perc is only meaningful when --source is given")
		return 1
	}

	if fset.Changed("simulate-loss-perc") && *sink == "" {
		printErrMsg("--simulate-loss-perc is only meaningful when --sink is given")
		return 1
	}

	if fset.Changed("source-frame") && *source == "" {
		printErrMsg("--source-frame is only meaningful when --source is given")
		return 1
	}

	if fset.Changed("sink-frame") && *sink == "" {
		printErrMsg("--sink-frame is only meaningful when --sink is given")
		return 1
	}

	if fset.Changed("jitter-buf") && *sink == "" {
		printErrMsg("--jitter-buf is only meaningful when --sink is given")
		return 1
	}

	if fset.Changed("max-drift") && *sink == "" {
		printErrMsg("--max-drift is only meaningful when --sink is given")
		return 1
	}

	if fset.Changed("stun") && fset.Changed("ice") {
		printErrMsg("--stun and --ice should not be used together")
		return 1
	}

	if fset.Changed("stun") {
		*ice = *stun
	}

	rtcParams := rtc.Params{
		IceURL:              *ice,
		MinPort:             minPort,
		MaxPort:             maxPort,
		OverrideIP:          *overrideIP,
		EnableWrite:         *source != "",
		EnableRead:          *sink != "",
		Rate:                int(*rate),
		Channels:            int(*channels),
		Mode:                mode,
		Complexity:          int(*complexity),
		LossPercent:         int(*lossPerc),
		SimulateLossPercent: int(*simLossPerc),
		Debug:               *debug,
	}

	if *answer {
		printMsg("Reading SDP offer from stdin...")
		var err error
		rtcParams.OfferSDP, err = readSDP()
		if err != nil {
			printErr(err)
			return 1
		}
	}

	printMsg("Creating WebRTC peer...")

	peer, err := rtc.NewPeer(rtcParams)
	if err != nil {
		printErr(err)
		return 1
	}

	defer func() {
		if err := peer.Close(); err != nil {
			printErr(err)
		}
	}()

	if *offer {
		printMsg("Writing SDP offer to stdout...")
		err := printSDP(peer.GetOffer())
		if err != nil {
			printErr(err)
			return 1
		}

		printMsg("Reading SDP answer from stdin...")
		answer, err := readSDP()
		if err != nil {
			printErr(err)
			return 1
		}
		if err := peer.SetAnswer(answer); err != nil {
			printErr(err)
			return 1
		}
	} else {
		printMsg("Writing SDP answer to stdout...")
		err := printSDP(peer.GetAnswer())
		if err != nil {
			printErr(err)
			return 1
		}
	}

	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	errCh := make(chan error, 32)
	eofCh := make(chan struct{})

	go func() {
		var state rtc.State
		var timeoutCh <-chan time.Time

		for {
			if state.IsConnected() {
				timeoutCh = nil
			} else if *timeout > 0 && timeoutCh == nil {
				timeoutCh = time.After(*timeout)
			}

			select {
			case state = <-peer.State():
				printMsg("ICE connection state changed to " + state.String())

			case <-timeoutCh:
				errCh <- fmt.Errorf("can't connect to remote peer during %s", *timeout)
				return
			}
		}
	}()

	if err := snd.SetPulseBufferSize(*pulseBuf); err != nil {
		printErr(err)
		return 1
	}

	if *source != "" {
		printMsg("Starting recording...")

		reader, err := snd.NewReader(snd.Params{
			DeviceOrFile: *source,
			Rate:         int(*rate),
			Channels:     int(*channels),
			FrameLength:  *sourceFrame,
		})
		if err != nil {
			printErr(err)
			return 1
		}

		defer reader.Stop()

		go func() {
			for b := range reader.Batches() {
				if b.Err != nil {
					errCh <- b.Err
					return
				}

				err := peer.Write(b.Data)
				if err != nil {
					errCh <- err
					return
				}
			}

			close(eofCh)
		}()
	}

	if *sink != "" {
		printMsg("Starting playback...")

		player, err := snd.NewPulsePlayer(snd.Params{
			DeviceOrFile: *sink,
			Rate:         int(*rate),
			Channels:     int(*channels),
		})
		if err != nil {
			printErr(err)
			return 1
		}

		defer player.Stop()

		go func() {
			for err := range player.Errors() {
				errCh <- err
			}
		}()

		jitbuf, err := dsp.NewJitterBuf(dsp.JitterBufParams{
			Rate:         int(*rate),
			Channels:     int(*channels),
			FrameLength:  *sinkFrame,
			BufferLength: *jitterBuf,
			MaxDrift:     *maxDrift,
			Debug:        *debug,
		})
		if err != nil {
			printErr(err)
			return 1
		}

		defer jitbuf.Stop()

		go func() {
			for {
				samples, err := peer.Read()
				if err != nil {
					errCh <- err
					return
				}

				jitbuf.Write(samples)
			}
		}()

		go func() {
			for {
				samples, err := jitbuf.Read()
				if err != nil {
					errCh <- err
					return
				}

				select {
				case player.Batches() <- samples:
				case <-player.Stopped():
					return
				}
			}
		}()
	}

	select {
	case <-eofCh:
		printMsg("Got EOF, exiting")
		return 0

	case <-sigCh:
		printMsg("Got interrupt, exiting")
		return 0

	case err := <-errCh:
		printErr(err)
		return 1
	}
}

func parsePorts(s string) (uint16, uint16, error) {
	slist := strings.Split(s, ":")
	if len(slist) != 2 {
		return 0, 0, errors.New("expected 'minport:maxport'")
	}

	minPort, err := strconv.Atoi(slist[0])
	if err != nil {
		return 0, 0, errors.New("not a number")
	}

	maxPort, err := strconv.Atoi(slist[1])
	if err != nil {
		return 0, 0, errors.New("not a number")
	}

	if minPort < 1 || minPort > 65535 {
		return 0, 0, errors.New("ports should be in range [1; 65535]")
	}

	if maxPort < 1 || maxPort > 65535 {
		return 0, 0, errors.New("ports should be in range [2; 65535]")
	}

	if minPort > maxPort {
		return 0, 0, errors.New("first port should not be greater than second")
	}

	return uint16(minPort), uint16(maxPort), nil
}

func parseMode(s string) (rtc.Mode, error) {
	switch s {
	case "voip":
		return rtc.ModeVoIP, nil
	case "audio":
		return rtc.ModeAudio, nil
	case "lowdelay":
		return rtc.ModeLowdelay, nil
	default:
		return rtc.Mode(-1), errors.New("should be voip|audio|lowdelay")
	}
}

func readSDP() (string, error) {
	tty := isatty.IsTerminal(os.Stdin.Fd()) && isatty.IsTerminal(os.Stderr.Fd())

	if tty {
		fmt.Fprintln(os.Stderr, delim1)
	}

	sdp, err := ioutil.ReadAll(os.Stdin)
	if err != nil {
		return "", fmt.Errorf("can't read sdp from stdin: %s", err.Error())
	}

	if tty {
		fmt.Fprintln(os.Stderr, delim2)
	}

	return string(sdp), nil
}

func printSDP(sdp string) error {
	tty := isatty.IsTerminal(os.Stdout.Fd()) && isatty.IsTerminal(os.Stderr.Fd())

	var err error
	if tty {
		_, err = fmt.Fprint(os.Stdout, delim1+"\n"+sdp+"\n"+delim2+"\n")
	} else {
		_, err = fmt.Fprint(os.Stdout, sdp+"\n")
		if err == nil {
			err = os.Stdout.Close()
		}
	}

	if err != nil {
		return fmt.Errorf("can't write to stdout: %s", err.Error())
	}

	return nil
}

func printErr(err error) {
	printErrMsg(err.Error())
}

func printErrMsg(s string) {
	printMsg("Error: " + s)
}

func printMsg(s string) {
	fmt.Fprintln(os.Stderr, s)
}
