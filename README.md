## Unreal Engine -> Pion WebRTC -> RTP Receiver

This is a proof of concept demonstrating pixel streaming from Unreal Engine to Pion's WebRTC, with Pion then forwarding RTP video/audio to FFPlay.

## Running the proof of concept

There are number of moving pieces to run this demo. We tested this proof of concept using **FFPlay 4.3.1**, **NodeJS 15.5.1**, **GoLang 1.15.6**, and **Unreal Engine 4.25** - we recommend using similar version numbers to reproduce our results.

1. Get the ["Pixel Streaming Demo"](https://docs.unrealengine.com/en-US/Resources/Showcases/PixelStreamingShowcase/index.html) and run that in Unreal Engine.
2. Run the Cirrus signalling server bundled with Unreal Engine by calling `run.bat` or `sudo node cirrus.js` in `Engine/Source/Programs/PixelStreaming/WebServers/SignallingAndWebServer/`.
3. Run this RTP forwarder, `go run main.go` in this repository. 
4. Play the forwarded video/audio streams in FFPlay, run `play-stream.bat` or `play-stream.sh`

## Configuring the forwarder
There are a number of flags that can be passed to `main.go` that may be of interest to those using this proof of concept:

```go
// CirrusPort - The port of the Cirrus signalling server that the Pixel Streaming instance is connected to.
--CirrusPort=80

// CirrusAddress - The address of the Cirrus signalling server that the Pixel Streaming instance is connected to.
--CirrusAddress="localhost"

// ForwardingAddress - The address to send the RTP stream to.
--ForwardingAddress="127.0.0.1"

// RTPVideoForwardingPort - The port to use for sending the RTP video stream.
--RTPVideoForwardingPort=4002

// RTPAudioForwardingPort - The port to use for sending the RTP audio stream.
--RTPAudioForwardingPort=4000

// RTPAudioPayloadType - The payload type of the RTP packet, 111 is OPUS.
--RTPAudioPayloadType=111

// RTPVideoPayloadType - The payload type of the RTP packet, 102 is H264.
--RTPVideoPayloadType=102
```

## Configuring FFPlay
Currently FFPlay is passed details about the RTP streams using the `rtp-forwarder.sdp` file.
Additionally, FFPlay is passed the `-fflags nobuffer -flags low_delay` flags to reduce latency; however, these may not be suitable in all cases.
