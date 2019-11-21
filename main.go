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

	stun := fset.String("stun", "stun:stun.l.google.com:19302", "STUN server URL")
	ports := fset.String("ports", "", "use specific UDP port range (e.g. \"3100:3200\")")

	rate := fset.Uint("rate", 48000, "sample rate")
	channels := fset.Uint("chans", 2, "# of channels")

	sourceFrame := fset.Duration("source-frame", 40*time.Millisecond, "source frame size")
	sinkFrame := fset.Duration("sink-frame", 20*time.Millisecond, "sink frame size")
	jitterBuf := fset.Duration("jitter-buf", 60*time.Millisecond, "jitter buffer size")
	pulseBuf := fset.Duration("pulse-buf", 20*time.Millisecond, "pulseaudio buffer size")

	maxDrift := fset.Duration("max-drift", 60*time.Millisecond,
		"maximum jitter buffer drift")

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
		printMsg("Error: exactly one of --offer and --answer options should be specified")
		return 1
	}

	var minPort, maxPort uint16
	var err error
	if *ports != "" {
		minPort, maxPort, err = parsePorts(*ports)
		if err != nil {
			printMsg("Error: invalid --ports: " + err.Error())
			return 1
		}
	}

	if *rate != 48000 && *rate != 96000 {
		printMsg("Error: --rate should be 48000 or 96000")
		return 1
	}

	if *channels != 1 && *channels != 2 {
		printMsg("Error: --chans should be 1 or 2")
		return 1
	}

	if *lossPerc > 100 {
		printMsg("Error: --loss-perc should be in [0; 100]")
		return 1
	}

	if *simLossPerc > 100 {
		printMsg("Error: --simulate-loss-perc should be in [0; 100]")
		return 1
	}

	if fset.Changed("loss-perc") && *source == "" {
		printMsg("Error: --loss-perc is only meaningful when --source is given")
		return 1
	}

	if fset.Changed("simulate-loss-perc") && *sink == "" {
		printMsg("Error: --simulate-loss-perc is only meaningful when --sink is given")
		return 1
	}

	if fset.Changed("jitter-buf") && *sink == "" {
		printMsg("Error: --jitter-buf is only meaningful when --sink is given")
		return 1
	}

	rtcParams := rtc.Params{
		StunURL:             *stun,
		MinPort:             minPort,
		MaxPort:             maxPort,
		EnableWrite:         *source != "",
		EnableRead:          *sink != "",
		Rate:                int(*rate),
		Channels:            int(*channels),
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

	if err := snd.SetPulseBufferSize(*pulseBuf); err != nil {
		printErr(err)
		return 1
	}

	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	errCh := make(chan error, 4)
	eofCh := make(chan struct{})

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
	fmt.Fprintln(os.Stderr, "Error: "+err.Error())
}

func printMsg(s string) {
	fmt.Fprintln(os.Stderr, s)
}
