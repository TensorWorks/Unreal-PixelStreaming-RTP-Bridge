// Heavily based on https://github.com/pion/webrtc/tree/master/examples/rtp-forwarder
// It has been modified to talk to Unreal Engine pixel streaming by sending an SDP offer.

package main

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"os"
	"strings"

	"net/url"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v3"
)

type udpConn struct {
	conn        *net.UDPConn
	port        int
	payloadType uint8
}

// Allows compressing offer/answer to bypass terminal input limits.
const compress = false

func writeWSMessage(wsConn *websocket.Conn, msg string) {
	err := wsConn.WriteMessage(websocket.TextMessage, []byte(msg))
	if err != nil {
		log.Println("Error writing websocket message: ", err)
	}
}

func createOffer(peerConnection *webrtc.PeerConnection) (string, error) {
	offer, err := peerConnection.CreateOffer(nil)
	if err != nil {
		log.Println("Error creating peer connection offer: ", err)
	}

	if err = peerConnection.SetLocalDescription(offer); err != nil {
		log.Println("Error setting local description of peer connection: ", err)
	}

	offerStringBytes, err := json.Marshal(offer)
	if err != nil {
		log.Println("Error unmarshalling json from offer object: ", err)
		return "", err
	}
	offerString := string(offerStringBytes)
	return offerString, err
}

func createPeerConnection() (*webrtc.PeerConnection, error) {
	// Create a MediaEngine object to configure the supported codec
	m := webrtc.MediaEngine{}

	// thing to try pion
	// could try trickle ice: a=ice-options:trickle
	// ours is missing a=rtcp:9 IN IP4 0.0.0.0
	// https://github.com/pion/webrtc/issues/925
	// https://github.com/pion/webrtc/issues/717

	// // Setup the codecs you want to use.
	// // We'll use a VP8 and Opus but you can also define your own
	if err := m.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{MimeType: "video/h264", ClockRate: 90000, Channels: 0, SDPFmtpLine: "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42e01f;x-google-start-bitrate=10000;x-google-max-bitrate=20000", RTCPFeedback: nil},
		PayloadType:        102,
	}, webrtc.RTPCodecTypeVideo); err != nil {
		return nil, err
	}
	if err := m.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{MimeType: "audio/opus", ClockRate: 48000, Channels: 2, SDPFmtpLine: "111 minptime=10;useinbandfec=1", RTCPFeedback: nil},
		PayloadType:        111,
	}, webrtc.RTPCodecTypeAudio); err != nil {
		return nil, err
	}

	// Create the API object with the MediaEngine
	api := webrtc.NewAPI(webrtc.WithMediaEngine(&m))

	// Prepare the configuration
	// UE is using unified plan on the backend so we should too
	config := webrtc.Configuration{SDPSemantics: webrtc.SDPSemanticsUnifiedPlan}

	// Create a new RTCPeerConnection
	peerConnection, err := api.NewPeerConnection(config)

	if err != nil {
		log.Println("Error making new peer connection: ", err)
		return nil, err
	}

	// Allow us to receive 1 audio track, and 1 video track in the "recvonly" mode
	if _, err = peerConnection.AddTransceiverFromKind(webrtc.RTPCodecTypeAudio, webrtc.RtpTransceiverInit{
		Direction: webrtc.RTPTransceiverDirectionRecvonly,
	}); err != nil {
		log.Println("Error adding RTP audio transceiver: ", err)
		return nil, err
	} else if _, err = peerConnection.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo, webrtc.RtpTransceiverInit{
		Direction: webrtc.RTPTransceiverDirectionRecvonly,
	}); err != nil {
		log.Println("Error adding RTP video transceiver: ", err)
		return nil, err
	}

	return peerConnection, err
}

func readLoop(wsConn *websocket.Conn) {
	// Start loop here to read web socket messages
	for {
		messageType, message, err := wsConn.ReadMessage()
		if err != nil {
			log.Printf("Websocket read error message: %v", err)
			break
		}
		stringMessage := string(message)
		toPrint := fmt.Sprintf("Received message, (type=%d): %s", messageType, stringMessage)
		log.Printf(toPrint)
	}
}

func main() {

	serverURL := url.URL{Scheme: "ws", Host: "localhost:80", Path: "/"}
	wsConn, _, err := websocket.DefaultDialer.Dial(serverURL.String(), nil)
	if err != nil {
		log.Fatal("Websocket dialing error: ", err)
		return
	}

	//close websocket on exit
	defer wsConn.Close()

	peerConnection, err := createPeerConnection()
	if err != nil {
		panic(err)
	}

	// Create the "offer" string that we will send over websocket to the signalling server
	offerString, err := createOffer(peerConnection)

	// Write a message over websocket
	// Will look something like this:
	//"{"type":"offer","sdp":"v=0\r\no=- 2927396662845926191 2 IN IP4 127.0.0.1\r\ns=-\r\nt=0 0\r\na=group:BUNDLE 0 1 2\r\na=msid-semantic: WMS\r\nm=audio 9 UDP/TLS/RTP/SAVPF 111 103 104 9 0 8 106 105 13 110 112 113 126\r\nc=IN IP4 0.0.0.0\r\na=rtcp:9 IN IP4 0.0.0.0\r\na=ice-ufrag:AAzT\r\na=ice-pwd:CwVMkLDd5lKoYUQL6z+b3jMF\r\na=ice-options:trickle\r\na=fingerprint:sha-256 F8:5B:E7:22:D9:91:2C:D5:FA:64:A6:6D:69:55:58:0A:EF:6D:0B:98:58:7A:A6:14:8D:31:68:94:CF:86:AF:E4\r\na=setup:actpass\r\na=mid:0\r\na=extmap:1 urn:ietf:params:rtp-hdrext:ssrc-audio-level\r\na=extmap:2 http://www.webrtc.org/experiments/rtp-hdrext/abs-send-time\r\na=extmap:3 http://www.ietf.org/id/draft-holmer-rmcat-transport-wide-cc-extensions-01\r\na=extmap:4 urn:ietf:params:rtp-hdrext:sdes:mid\r\na=extmap:5 urn:ietf:params:rtp-hdrext:sdes:rtp-stream-id\r\na=extmap:6 urn:ietf:params:rtp-hdrext:sdes:repaired-rtp-stream-id\r\na=recvonly\r\na=rtcp-mux\r\na=rtpmap:111 opus/48000/2\r\na=rtcp-fb:111 transport-cc\r\na=fmtp:111 minptime=10;useinbandfec=1\r\na=rtpmap:103 ISAC/16000\r\na=rtpmap:104 ISAC/32000\r\na=rtpmap:9 G722/8000\r\na=rtpmap:0 PCMU/8000\r\na=rtpmap:8 PCMA/8000\r\na=rtpmap:106 CN/32000\r\na=rtpmap:105 CN/16000\r\na=rtpmap:13 CN/8000\r\na=rtpmap:110 telephone-event/48000\r\na=rtpmap:112 telephone-event/32000\r\na=rtpmap:113 telephone-event/16000\r\na=rtpmap:126 telephone-event/8000\r\nm=video 9 UDP/TLS/RTP/SAVPF 96 97 98 99 100 101 122 102 121 127 120 125 107 108 109 124 119 123 118 114 115 116\r\nc=IN IP4 0.0.0.0\r\na=rtcp:9 IN IP4 0.0.0.0\r\na=ice-ufrag:AAzT\r\na=ice-pwd:CwVMkLDd5lKoYUQL6z+b3jMF\r\na=ice-options:trickle\r\na=fingerprint:sha-256 F8:5B:E7:22:D9:91:2C:D5:FA:64:A6:6D:69:55:58:0A:EF:6D:0B:98:58:7A:A6:14:8D:31:68:94:CF:86:AF:E4\r\na=setup:actpass\r\na=mid:1\r\na=extmap:14 urn:ietf:params:rtp-hdrext:toffset\r\na=extmap:2 http://www.webrtc.org/experiments/rtp-hdrext/abs-send-time\r\na=extmap:13 urn:3gpp:video-orientation\r\na=extmap:3 http://www.ietf.org/id/draft-holmer-rmcat-transport-wide-cc-extensions-01\r\na=extmap:12 http://www.webrtc.org/experiments/rtp-hdrext/playout-delay\r\na=extmap:11 http://www.webrtc.org/experiments/rtp-hdrext/video-content-type\r\na=extmap:7 http://www.webrtc.org/experiments/rtp-hdrext/video-timing\r\na=extmap:8 http://www.webrtc.org/experiments/rtp-hdrext/color-space\r\na=extmap:4 urn:ietf:params:rtp-hdrext:sdes:mid\r\na=extmap:5 urn:ietf:params:rtp-hdrext:sdes:rtp-stream-id\r\na=extmap:6 urn:ietf:params:rtp-hdrext:sdes:repaired-rtp-stream-id\r\na=recvonly\r\na=rtcp-mux\r\na=rtcp-rsize\r\na=rtpmap:96 VP8/90000\r\na=rtcp-fb:96 goog-remb\r\na=rtcp-fb:96 transport-cc\r\na=rtcp-fb:96 ccm fir\r\na=rtcp-fb:96 nack\r\na=rtcp-fb:96 nack pli\r\na=rtpmap:97 rtx/90000\r\na=fmtp:97 apt=96\r\na=rtpmap:98 VP9/90000\r\na=rtcp-fb:98 goog-remb\r\na=rtcp-fb:98 transport-cc\r\na=rtcp-fb:98 ccm fir\r\na=rtcp-fb:98 nack\r\na=rtcp-fb:98 nack pli\r\na=fmtp:98 profile-id=0\r\na=rtpmap:99 rtx/90000\r\na=fmtp:99 apt=98\r\na=rtpmap:100 VP9/90000\r\na=rtcp-fb:100 goog-remb\r\na=rtcp-fb:100 transport-cc\r\na=rtcp-fb:100 ccm fir\r\na=rtcp-fb:100 nack\r\na=rtcp-fb:100 nack pli\r\na=fmtp:100 profile-id=2\r\na=rtpmap:101 rtx/90000\r\na=fmtp:101 apt=100\r\na=rtpmap:122 VP9/90000\r\na=rtcp-fb:122 goog-remb\r\na=rtcp-fb:122 transport-cc\r\na=rtcp-fb:122 ccm fir\r\na=rtcp-fb:122 nack\r\na=rtcp-fb:122 nack pli\r\na=fmtp:122 profile-id=1\r\na=rtpmap:102 H264/90000\r\na=rtcp-fb:102 goog-remb\r\na=rtcp-fb:102 transport-cc\r\na=rtcp-fb:102 ccm fir\r\na=rtcp-fb:102 nack\r\na=rtcp-fb:102 nack pli\r\na=fmtp:102 level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42001f;x-google-start-bitrate=10000;x-google-max-bitrate=20000\r\na=rtpmap:121 rtx/90000\r\na=fmtp:121 apt=102\r\na=rtpmap:127 H264/90000\r\na=rtcp-fb:127 goog-remb\r\na=rtcp-fb:127 transport-cc\r\na=rtcp-fb:127 ccm fir\r\na=rtcp-fb:127 nack\r\na=rtcp-fb:127 nack pli\r\na=fmtp:127 level-asymmetry-allowed=1;packetization-mode=0;profile-level-id=42001f;x-google-start-bitrate=10000;x-google-max-bitrate=20000\r\na=rtpmap:120 rtx/90000\r\na=fmtp:120 apt=127\r\na=rtpmap:125 H264/90000\r\na=rtcp-fb:125 goog-remb\r\na=rtcp-fb:125 transport-cc\r\na=rtcp-fb:125 ccm fir\r\na=rtcp-fb:125 nack\r\na=rtcp-fb:125 nack pli\r\na=fmtp:125 level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42e01f;x-google-start-bitrate=10000;x-google-max-bitrate=20000\r\na=rtpmap:107 rtx/90000\r\na=fmtp:107 apt=125\r\na=rtpmap:108 H264/90000\r\na=rtcp-fb:108 goog-remb\r\na=rtcp-fb:108 transport-cc\r\na=rtcp-fb:108 ccm fir\r\na=rtcp-fb:108 nack\r\na=rtcp-fb:108 nack pli\r\na=fmtp:108 level-asymmetry-allowed=1;packetization-mode=0;profile-level-id=42e01f;x-google-start-bitrate=10000;x-google-max-bitrate=20000\r\na=rtpmap:109 rtx/90000\r\na=fmtp:109 apt=108\r\na=rtpmap:124 H264/90000\r\na=rtcp-fb:124 goog-remb\r\na=rtcp-fb:124 transport-cc\r\na=rtcp-fb:124 ccm fir\r\na=rtcp-fb:124 nack\r\na=rtcp-fb:124 nack pli\r\na=fmtp:124 level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=4d001f;x-google-start-bitrate=10000;x-google-max-bitrate=20000\r\na=rtpmap:119 rtx/90000\r\na=fmtp:119 apt=124\r\na=rtpmap:123 H264/90000\r\na=rtcp-fb:123 goog-remb\r\na=rtcp-fb:123 transport-cc\r\na=rtcp-fb:123 ccm fir\r\na=rtcp-fb:123 nack\r\na=rtcp-fb:123 nack pli\r\na=fmtp:123 level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=64001f;x-google-start-bitrate=10000;x-google-max-bitrate=20000\r\na=rtpmap:118 rtx/90000\r\na=fmtp:118 apt=123\r\na=rtpmap:114 red/90000\r\na=rtpmap:115 rtx/90000\r\na=fmtp:115 apt=114\r\na=rtpmap:116 ulpfec/90000\r\nm=application 9 UDP/DTLS/SCTP webrtc-datachannel\r\nc=IN IP4 0.0.0.0\r\na=ice-ufrag:AAzT\r\na=ice-pwd:CwVMkLDd5lKoYUQL6z+b3jMF\r\na=ice-options:trickle\r\na=fingerprint:sha-256 F8:5B:E7:22:D9:91:2C:D5:FA:64:A6:6D:69:55:58:0A:EF:6D:0B:98:58:7A:A6:14:8D:31:68:94:CF:86:AF:E4\r\na=setup:actpass\r\na=mid:2\r\na=sctp-port:5000\r\na=max-message-size:262144\r\n"}"
	writeWSMessage(wsConn, offerString)
	log.Println("Sent offer!")

	readLoop(wsConn)

	// // Prepare the configuration
	// config := webrtc.Configuration{
	// 	ICEServers: []webrtc.ICEServer{
	// 		{
	// 			URLs: []string{"stun:stun.l.google.com:19302"},
	// 		},
	// 	},
	// }

	// // Create a local addr
	// var laddr *net.UDPAddr
	// if laddr, err = net.ResolveUDPAddr("udp", "127.0.0.1:"); err != nil {
	// 	panic(err)
	// }

	// // Prepare udp conns
	// // Also update incoming packets with expected PayloadType, the browser may use
	// // a different value. We have to modify so our stream matches what rtp-forwarder.sdp expects
	// udpConns := map[string]*udpConn{
	// 	"audio": {port: 4000, payloadType: 111},
	// 	"video": {port: 4002, payloadType: 96},
	// }
	// for _, c := range udpConns {
	// 	// Create remote addr
	// 	var raddr *net.UDPAddr
	// 	if raddr, err = net.ResolveUDPAddr("udp", fmt.Sprintf("127.0.0.1:%d", c.port)); err != nil {
	// 		panic(err)
	// 	}

	// 	// Dial udp
	// 	if c.conn, err = net.DialUDP("udp", laddr, raddr); err != nil {
	// 		panic(err)
	// 	}
	// 	defer func(conn net.PacketConn) {
	// 		if closeErr := conn.Close(); closeErr != nil {
	// 			panic(closeErr)
	// 		}
	// 	}(c.conn)
	// }

	// // Set a handler for when a new remote track starts, this handler will forward data to
	// // our UDP listeners.
	// // In your application this is where you would handle/process audio/video
	// peerConnection.OnTrack(func(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
	// 	// Retrieve udp connection
	// 	c, ok := udpConns[track.Kind().String()]
	// 	if !ok {
	// 		return
	// 	}

	// 	// Send a PLI on an interval so that the publisher is pushing a keyframe every rtcpPLIInterval
	// 	go func() {
	// 		ticker := time.NewTicker(time.Second * 2)
	// 		for range ticker.C {
	// 			if rtcpErr := peerConnection.WriteRTCP([]rtcp.Packet{&rtcp.PictureLossIndication{MediaSSRC: uint32(track.SSRC())}}); rtcpErr != nil {
	// 				fmt.Println(rtcpErr)
	// 			}
	// 		}
	// 	}()

	// 	b := make([]byte, 1500)
	// 	rtpPacket := &rtp.Packet{}
	// 	for {
	// 		// Read
	// 		n, _, readErr := track.Read(b)
	// 		if readErr != nil {
	// 			panic(readErr)
	// 		}

	// 		// Unmarshal the packet and update the PayloadType
	// 		if err = rtpPacket.Unmarshal(b[:n]); err != nil {
	// 			panic(err)
	// 		}
	// 		rtpPacket.PayloadType = c.payloadType

	// 		// Marshal into original buffer with updated PayloadType
	// 		if n, err = rtpPacket.MarshalTo(b); err != nil {
	// 			panic(err)
	// 		}

	// 		// Write
	// 		if _, err = c.conn.Write(b[:n]); err != nil {
	// 			// For this particular example, third party applications usually timeout after a short
	// 			// amount of time during which the user doesn't have enough time to provide the answer
	// 			// to the browser.
	// 			// That's why, for this particular example, the user first needs to provide the answer
	// 			// to the browser then open the third party application. Therefore we must not kill
	// 			// the forward on "connection refused" errors
	// 			if opError, ok := err.(*net.OpError); ok && opError.Err.Error() == "write: connection refused" {
	// 				continue
	// 			}
	// 			panic(err)
	// 		}
	// 	}
	// })

	// // Create context
	// ctx, cancel := context.WithCancel(context.Background())

	// // Set the handler for ICE connection state
	// // This will notify you when the peer has connected/disconnected
	// peerConnection.OnICEConnectionStateChange(func(connectionState webrtc.ICEConnectionState) {
	// 	fmt.Printf("Connection State has changed %s \n", connectionState.String())

	// 	if connectionState == webrtc.ICEConnectionStateConnected {
	// 		fmt.Println("Ctrl+C the remote client to stop the demo")
	// 	} else if connectionState == webrtc.ICEConnectionStateFailed ||
	// 		connectionState == webrtc.ICEConnectionStateDisconnected {
	// 		fmt.Println("Done forwarding")
	// 		cancel()
	// 	}
	// })

	// // Wait for the offer to be pasted
	// offer := webrtc.SessionDescription{}
	// Decode(MustReadStdin(), &offer)

	// // Set the remote SessionDescription
	// if err = peerConnection.SetRemoteDescription(offer); err != nil {
	// 	panic(err)
	// }

	// // Create answer
	// answer, err := peerConnection.CreateAnswer(nil)
	// if err != nil {
	// 	panic(err)
	// }

	// // Create channel that is blocked until ICE Gathering is complete
	// gatherComplete := webrtc.GatheringCompletePromise(peerConnection)

	// // Sets the LocalDescription, and starts our UDP listeners
	// if err = peerConnection.SetLocalDescription(answer); err != nil {
	// 	panic(err)
	// }

	// // Block until ICE Gathering is complete, disabling trickle ICE
	// // we do this because we only can exchange one signaling message
	// // in a production application you should exchange ICE Candidates via OnICECandidate
	// <-gatherComplete

	// // Output the answer in base64 so we can paste it in browser
	// fmt.Println(Encode(*peerConnection.LocalDescription()))

	// // Wait for context to be done
	// <-ctx.Done()
}

////////////////////////////
// INTERNAL FUNCTIONS
////////////////////////////

// MustReadStdin blocks until input is received from stdin
func MustReadStdin() string {
	r := bufio.NewReader(os.Stdin)

	var in string
	for {
		var err error
		in, err = r.ReadString('\n')
		if err != io.EOF {
			if err != nil {
				panic(err)
			}
		}
		in = strings.TrimSpace(in)
		if len(in) > 0 {
			break
		}
	}

	fmt.Println("")

	return in
}

// Encode encodes the input in base64
// It can optionally zip the input before encoding
func Encode(obj interface{}) string {
	b, err := json.Marshal(obj)
	if err != nil {
		panic(err)
	}

	if compress {
		b = zip(b)
	}

	return base64.StdEncoding.EncodeToString(b)
}

// Decode decodes the input from base64
// It can optionally unzip the input after decoding
func Decode(in string, obj interface{}) {
	b, err := base64.StdEncoding.DecodeString(in)
	if err != nil {
		panic(err)
	}

	if compress {
		b = unzip(b)
	}

	err = json.Unmarshal(b, obj)
	if err != nil {
		panic(err)
	}
}

func zip(in []byte) []byte {
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	_, err := gz.Write(in)
	if err != nil {
		panic(err)
	}
	err = gz.Flush()
	if err != nil {
		panic(err)
	}
	err = gz.Close()
	if err != nil {
		panic(err)
	}
	return b.Bytes()
}

func unzip(in []byte) []byte {
	var b bytes.Buffer
	_, err := b.Write(in)
	if err != nil {
		panic(err)
	}
	r, err := gzip.NewReader(&b)
	if err != nil {
		panic(err)
	}
	res, err := ioutil.ReadAll(r)
	if err != nil {
		panic(err)
	}
	return res
}
