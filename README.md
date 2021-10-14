## Unreal Engine -> Pion WebRTC -> RTP Receiver

This is a proof of concept demonstrating pixel streaming from Unreal Engine to Pion's WebRTC, with Pion then forwarding RTP video/audio to FFPlay.

## Running the proof of concept

There are number of moving pieces to run this demo. We tested this proof of concept using **FFPlay 4.3.1**, **NodeJS 15.5.1**, **GoLang 1.15.6**, and **Unreal Engine 4.25** - we recommend using similar version numbers to reproduce our results.

1. Get the ["Pixel Streaming Demo"](https://docs.unrealengine.com/en-US/Resources/Showcases/PixelStreamingShowcase/index.html) and run that in Unreal Engine.
2. Run the Cirrus signalling server bundled with Unreal Engine by calling `run.bat` or `sudo node cirrus.js` in `Samples\PixelStreaming\WebServers\SignallingWebServer`.
3. Run this RTP forwarder, `go run main.go` in this repository. 
4. Play the forwarded video/audio streams in FFPlay, run `play-stream.bat` or `play-stream.sh`

## Configuring the forwarder
There are a number of flags that can be passed to `main.go` that may be of interest to those using this proof of concept:

```go
// CirrusPort - The port of the Cirrus signalling server that the Pixel Streaming instance is connected to.
var CirrusPort = flag.Int("CirrusPort", 80, "The port of the Cirrus signalling server that the Pixel Streaming instance is connected to.")

// CirrusAddress - The address of the Cirrus signalling server that the Pixel Streaming instance is connected to.
var CirrusAddress = flag.String("CirrusAddress", "localhost", "The address of the Cirrus signalling server that the Pixel Streaming instance is connected to.")

// ForwardingAddress - The address to send the RTP stream to.
var ForwardingAddress = flag.String("ForwardingAddress", "127.0.0.1", "The address to send the RTP stream to.")

// RTPVideoForwardingPort - The port to use for sending the RTP video stream.
var RTPVideoForwardingPort = flag.Int("RTPVideoForwardingPort", 4002, "The port to use for sending the RTP video stream.")

// RTPAudioForwardingPort - The port to use for sending the RTP audio stream.
var RTPAudioForwardingPort = flag.Int("RTPAudioForwardingPort", 4000, "The port to use for sending the RTP audio stream.")

// RTPAudioPayloadType - The payload type of the RTP packet, 111 is OPUS.
var RTPAudioPayloadType = flag.Uint("RTPAudioPayloadType", 111, "The payload type of the RTP packet, 111 is OPUS.")

// RTPVideoPayloadType - The payload type of the RTP packet, 125 is H264 constrained baseline 2.0 in Chrome, with packetization mode of 1.
var RTPVideoPayloadType = flag.Uint("RTPVideoPayloadType", 125, "The payload type of the RTP packet, 125 is H264 constrained baseline in Chrome.")

// RTCPIntervalMs - How often (ms) to send RTCP messages (such as REMB, PLI)
var RTCPIntervalMs = flag.Int("RTCPIntervalMs", 2000, "How often (ms) to send RTCP message such as REMB, PLI.")

//Whether or not to send PLI messages on an interval.
var RTCPSendPLI = flag.Bool("RTCPSendPLI", true, "Whether or not to send PLI messages on an interval.")

//Whether or not to send REMB messages on an interval.
var RTCPSendREMB = flag.Bool("RTCPSendREMB", true, "Whether or not to send REMB messages on an interval.")

// Receiver-side estimated maximum bitrate.
var REMB = flag.Uint64("REMB", 400000000, "Receiver-side estimated maximum bitrate.")
```

## Configuring FFPlay
You may need to download FFPlay if it is not on your system already: https://ffmpeg.org/ffplay.html
Currently FFPlay is passed details about the RTP streams using the `rtp-forwarder.sdp` file.
Additionally, FFPlay is passed the `-fflags nobuffer -flags low_delay` flags to reduce latency; however, these may not be suitable in all cases.
