# WebRTC command-line peer

[![Build Status](https://travis-ci.org/gavv/webrtc-cli.svg?branch=master)](https://travis-ci.org/gavv/webrtc-cli)

`webrtc-cli` is a small command-line tool allowing to stream to and from audio devices and files via WebRTC.

Features:

* Generating SDP offers and answers.
* Connecting incoming and outgoing WebRTC tracks with local audio devices and files.
* Unidirectional and bidirectional operation.
* Restoring lost packets using Opus FEC and PLC.

## What is supported

Configurations:

* single incoming and/or single outcoming track

Media types:

* audio

Audio devices:

* PulseAudio sources and sinks

File formats:

* WAV files

RTP codecs:

* Opus codec

Operating systems:

* tested only on Linux

## Installation

#### From sources

Install dependencies:

```
$ sudo apt-get install gcc make pkg-config libopus-dev libopusfile-dev libpulse-dev
```

Install [recent Go](https://github.com/golang/go/wiki/Ubuntu) (at least 1.12 is needed):

```
$ sudo add-apt-repository ppa:longsleep/golang-backports
$ sudo apt-get update
$ sudo apt-get install golang-go
```

Clone and build:

```
$ git clone https://github.com/gavv/webrtc-cli.git
$ cd webrtc-cli
$ make
```

Run the tool:

```
$ ./webrtc-cli -h
```

You can also install the tool system-wide, e.g.:

```
$ sudo cp ./webrtc-cli /usr/local/bin
```

#### Using go get

Alternatively, you can install `webrtc-cli` into GOPATH using `go get`.

First make sure that the Go version is at least 1.12:

```
$ go version
```

And then run this command:

```
$ go get -v github.com/gavv/webrtc-cli
```

It will automatically fetch sources and build and install `webrtc-cli` executable into `$GOPATH/bin` directory or into `~/go/bin` if `$GOPATH` is not set.

## Options

```
$ webrtc-cli --help
Usage of webrtc-cli:
      --offer                     enable offer mode
      --answer                    enable answer mode
      --source string             pulseaudio source or input wav file
      --sink string               pulseaudio sink
      --timeout duration          exit if can't connect during timeout
      --ice string                STUN or TURN server URL (default "stun:stun.l.google.com:19302")
      --ports string              use specific UDP port range (e.g. "3100:3200")
      --override-ip string        override IP address in SDP offer/answer
      --rate uint                 sample rate (default 48000)
      --chans uint                # of channels (default 2)
      --source-frame duration     source frame size (default 40ms)
      --sink-frame duration       sink frame size (default 40ms)
      --jitter-buf duration       jitter buffer size (default 120ms)
      --pulse-buf duration        pulseaudio buffer size (default 20ms)
      --max-drift duration        maximum jitter buffer drift (default 30ms)
      --mode string               opus encoder mode: voip|audio|lowdelay (default "voip")
      --complexity uint           opus encoder complexity (default 10)
      --loss-perc uint            expected packet loss percent, passed to opus encoder (default 25)
      --simulate-loss-perc uint   simulate given loss percent when receiving packets
      --debug                     enable more logs
```

## Operation

The tool works in one of the two operation modes:

* **Offer mode:** the tool generates an SDP offer and prints it to stdout. Then the tool reads an SDP answer from stdin. Then the tool starts streaming.

* **Answer mode:** the tool reads an SDP offer from stdin. Then the tool generates an SDP answer and prints it to stdout. Then the tool starts streaming.

In both modes, the user is responsible to deliver SDP offer and answer between the two peers (e.g. between the two instances of the tool or between the tool and the browser).

When the tool reads SDP offer or answer from stdin, it reads all bytes until EOF is reached. If you're manually pasting it in the terminal, press ^D after pasting the text.

The tool may also work in one of the two direction modes:

* **Unidirectional mode:** only a source or only a sink is specified.

* **Bidirectional mode:** both a source and a sink are specified.

It does not matter what peer is generating an offer or answer and what peer has a sink or source or both. All combinations are allowed.

## Latency

Recording (source) latency is the sum of:

* PulseAudio buffer size
* source frame size, also used as the Opus packet size

Playback (sink) latency is the sum of:

* jitter buffer size
* PulseAudio buffer size
* sink frame size

The overall latency is the sum of recoding latency, network latency, and playback latency.

The following frame sizes are supported: 10ms, 20ms, 40ms, and 60ms.

To employ FEC, jitter buffer should be at least two packet sizes. However, for seamless playback, it is recommended to set it to three packet sizes. The maximum drift parameter specifies how much the actual jitter buffer size may differ from the configured size.

PulseAudio usually doesn't handle very low latencies well. It's recommended to set PulseAudio buffer size at least to 20ms.

## Limitations

* This tool does not implement clock drift compensation. Instead, it monitors the incoming queue size and just restarts the stream when the queue size goes out of bounds. This is quite unnoticeable for speech, but may be annoying for music.

* Lost packets are recovered using Opus FEC (Forward Erasure Correction) and Opus PLC (Packet Loss Concealment). Opus FEC recovers packets from a redundant lower-bitrate stream, and PLC recreates packets using interpolation. Again, these methods work pretty good for speech, but may be annoying for music.

* PLC is triggered only when a packet arrives. If jitter buffer is empty and no packets have arrived yet, zero samples will be produced instead of PLC. This problem usually arises only on high packet loss ratios.

* I didn't try to perform any optimizations. Likely, the tool will not handle very low latencies well.

## Browser demo

This repo also provides [WebRTC demo](https://gavv.github.io/webrtc-cli/) ([source code](docs/index.html)). It contains a sample JavaScript code that can interact with this tool. The demo was tested on the Chromium browser.

If you allow it to access the microphone, it will play sound from the remote peer and send sound from the microphone to the remote peer. Otherwise, it will only play sound from the remote peer.

## Examples

#### Stream from WAV file to PulseAudio sink

First peer:

```
$ webrtc-cli --offer --source ./test.wav
```

Second peer:

```
$ webrtc-cli --answer --sink alsa_output.pci-0000_00_1f.3.analog-stereo
```

#### Stream from PulseAudio source to PulseAudio sink

First peer:

```
$ webrtc-cli --offer --source alsa_input.pci-0000_00_1f.3.analog-stereo
```

Second peer:

```
$ webrtc-cli --answer --sink alsa_output.usb-Burr-Brown_from_TI_USB_Audio_CODEC-00.analog-stereo
```

#### Bidirectional streaming

First peer:

```
$ webrtc-cli --offer \
    --source alsa_input.pci-0000_00_1f.3.analog-stereo \
    --sink alsa_input.pci-0000_00_1f.3.analog-stereo
```

Second peer:

```
$ webrtc-cli --answer \
    --source alsa_output.usb-Burr-Brown_from_TI_USB_Audio_CODEC-00.analog-stereo \
    --sink alsa_output.usb-Burr-Brown_from_TI_USB_Audio_CODEC-00.analog-stereo
```

#### Stream between web browser and webrtc-cli

First peer: [WebRTC demo](https://gavv.github.io/webrtc-cli/) ([source code](docs/index.html))

Second peer:

```
$ webrtc-cli --answer \
    --source alsa_output.usb-Burr-Brown_from_TI_USB_Audio_CODEC-00.analog-stereo \
    --sink alsa_output.usb-Burr-Brown_from_TI_USB_Audio_CODEC-00.analog-stereo
```

#### Use lower latency

```
$ webrtc-cli \
    --pulse-buf 20ms \
    --source-frame 10ms --sink-frame 10ms \
    --jitter-buf 20ms --max-drift 20ms \
    ...
```

#### Force specific IP address and UDP port range

```
$ webrtc-cli --offer --override-ip 93.184.216.34 --ports 5100:5200 ...
```

This will restrict what UDP ports can be used to given range and replace IP addresses of all ICE candidates in generated SDP offer with given IP.

## Dependencies

Build tools:

* Go >= 1.12
* GCC (for cgo)
* pkg-config (for cgo)

Go libraries:

* [pion/webrtc](https://github.com/pion/webrtc) (pure Go WebRTC implementation)
* [gavv/opus](https://github.com/gavv/opus), forked from [hraban/opus](https://github.com/hraban/opus) (Go bindings for libopus)
* [mesilliac/pulse-simple](https://github.com/mesilliac/pulse-simple) (Go bindings for libpulse-simple)
* [youpy/go-wav](https://github.com/youpy/go-wav) (pure Go WAVE file library)
* [spf13/pflag](github.com/spf13/pflag) (command-line parsing library)
* [mattn/go-isatty](isatty.IsTerminal(os.Stdout.Fd())) (isatty function for Go)
* [x/time/rate](https://github.com/golang/time) (rate-limiter library)

C libraries:

* libopus and libopusfile
* libpulse-simple (part of PulseAudio)

## Acknowledgments

This tool was initially developed for a freelance project. Big thanks to my customer who has kindly allowed to open-source it!

I'm working on another related open-source project, [Roc Toolkit](https://github.com/roc-streaming/roc-toolkit). It offers a wider functionality and better service quality, but so far has no WebRTC support.

## Contributing

Feel free to report bugs, suggest improvements, and send pull requests!

Build:

```
$ make
```

Run checks:

```
$ make check
```

Format code:

```
$ make fmt
```

## Authors

See [here](https://github.com/gavv/webrtc-cli/graphs/contributors).

## License

[MIT](LICENSE)
