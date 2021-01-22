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

type ueICECandidateResp struct {
	Type      string                  `json:"type"`
	Candidate webrtc.ICECandidateInit `json:"candidate"`
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

	// // Setup the codecs you want to use.
	// // We'll use a H264 and Opus but you can also define your own
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

// Pion has recieved an "answer" from the remote Unreal Engine Pixel Streaming (through Cirrus)
// Pion will now set its remote session description that it got from the answer.
// Once Pion has its own local session description and the remote session description set
// then it should begin signalling the ice candidates it got from the Unreal Engine side.
// This flow is based on:
// https://github.com/pion/webrtc/blob/687d915e05a69441beae1bba0802e28756eecbbc/examples/pion-to-pion/offer/main.go#L90
func handleRemoteAnswer(message []byte, peerConnection *webrtc.PeerConnection) {
	sdp := webrtc.SessionDescription{}
	unmarshalError := json.Unmarshal([]byte(message), &sdp)

	if unmarshalError != nil {
		log.Printf("Error occured during unmarshaling sdp. Error: %s", unmarshalError.Error())
		return
	}

	// Set remote session description we got from UE pixel streaming
	if sdpErr := peerConnection.SetRemoteDescription(sdp); sdpErr != nil {
		log.Printf("Error occured setting remote session description. Error: %s", sdpErr.Error())
		return
	}

	//ToDo: signal remote ice candidates
	println("ToDo: We need to signal remote ice candidates after we send our answer")
}

// Pion has received an ice candidate from the remote Unreal Engine Pixel Streaming (through Cirrus).
// We parse this message and add that ice candidate to our peer connection.
// Flow based on: https://github.com/pion/webrtc/blob/687d915e05a69441beae1bba0802e28756eecbbc/examples/pion-to-pion/offer/main.go#L82
func handleRemoteIceCandidate(message []byte, peerConnection *webrtc.PeerConnection) {
	var iceCandidateInit webrtc.ICECandidateInit
	jsonErr := json.Unmarshal(message, &iceCandidateInit)
	if jsonErr != nil {
		log.Printf("Error unmarshaling ice candidate. Error: %s", jsonErr.Error())
		return
	}

	// The actual adding of the remote ice candidate happens here.
	if candidateErr := peerConnection.AddICECandidate(iceCandidateInit); candidateErr != nil {
		log.Printf("Error adding remote ice candidate. Error: %s", candidateErr.Error())
		return
	}

	fmt.Println(fmt.Sprintf("Added remote ice candidate from UE - %s", iceCandidateInit.Candidate))
}

// Starts an infinite loop where we poll for new websocket messages and react to them.
func startControlLoop(wsConn *websocket.Conn, peerConnection *webrtc.PeerConnection) {
	// Start loop here to read web socket messages
	for {
		messageType, message, err := wsConn.ReadMessage()
		if err != nil {
			log.Printf("Websocket read error message: %v", err)
			continue
		}
		stringMessage := string(message)

		// We print the recieved messages in a different colour so they are easier to distinguish.
		colorGreen := "\033[32m"
		colorReset := "\033[0m"
		fmt.Println(string(colorGreen), fmt.Sprintf("Received message, (type=%d): %s", messageType, stringMessage), string(colorReset))

		// Transform the raw bytes into a map of string: []byte pairs, we can unmarshall each key/value as needed.
		var objmap map[string]json.RawMessage
		err = json.Unmarshal(message, &objmap)

		if err != nil {
			log.Printf("Error unmarshalling bytes from websocket message. Error: %s", err.Error())
			continue
		}

		// Get the type of message we received from the Unreal Engine side
		var pixelStreamingMessageType string
		err = json.Unmarshal(objmap["type"], &pixelStreamingMessageType)

		if err != nil {
			log.Printf("Error unmarshaling type from pixel streaming message. Error: %s", err.Error())
			continue
		}

		// Based on the "type" of message we received, we react accordingly.
		switch pixelStreamingMessageType {
		case "playerCount":
			var playerCount int
			err = json.Unmarshal(objmap["count"], &playerCount)
			if err != nil {
				log.Printf("Error unmarshaling player count. Error: %s", err.Error())
			}
			fmt.Println(fmt.Sprintf("Player count is: %d", playerCount))
		case "config":
			fmt.Println("Got config message, ToDO: react based on config that was passed.")
		case "answer":
			handleRemoteAnswer(message, peerConnection)
		case "iceCandidate":
			candidateMsg := objmap["candidate"]
			handleRemoteIceCandidate(candidateMsg, peerConnection)
		default:
			log.Println("Got message we do not specifically handle, type was: " + pixelStreamingMessageType)
		}

	}
}

// Send an "offer" string over websocket to Unreal Engine to start the WebRTC handshake
func sendOffer(wsConn *websocket.Conn, peerConnection *webrtc.PeerConnection) {

	offerString, err := createOffer(peerConnection)

	if err != nil {
		log.Printf("Error creating offer. Error: %s", err.Error())
	} else {
		// Write our offer over websocket: "{"type":"offer","sdp":"v=0\r\no=- 2927396662845926191 2 IN IP4 127.0.0.1....."
		writeWSMessage(wsConn, offerString)
		fmt.Println("Sending offer...")
		fmt.Println(offerString)
	}
}

// Send our local ICE candidate to Unreal Engine using websockets.
func sendLocalIceCandidate(wsConn *websocket.Conn, localIceCandidate *webrtc.ICECandidate) {
	var iceCandidateInit webrtc.ICECandidateInit = localIceCandidate.ToJSON()
	var respPayload ueICECandidateResp = ueICECandidateResp{Type: "iceCandidate", Candidate: iceCandidateInit}

	jsonPayload, err := json.Marshal(respPayload)

	if err != nil {
		log.Printf("Error turning local ice candidate into JSON. Error: %s", err.Error())
	}

	jsonStr := string(jsonPayload)
	writeWSMessage(wsConn, jsonStr)
	fmt.Println("Sending our local ice candidate to UE...")
	fmt.Println(jsonStr)
}

func main() {

	serverURL := url.URL{Scheme: "ws", Host: "localhost:80", Path: "/"}
	wsConn, _, err := websocket.DefaultDialer.Dial(serverURL.String(), nil)
	if err != nil {
		log.Fatal("Websocket dialing error: ", err)
		return
	}

	defer wsConn.Close()

	peerConnection, err := createPeerConnection()
	if err != nil {
		panic(err)
	}

	// Store our local ice candidates that we will transmit to UE
	//var candidatesMux sync.Mutex
	pendingCandidates := make([]*webrtc.ICECandidate, 0)

	// Setup a callback to capture our local ice candidates when they are ready
	peerConnection.OnICECandidate(func(localIceCandidate *webrtc.ICECandidate) {
		if localIceCandidate == nil {
			return
		}

		//candidatesMux.Lock()
		//defer candidatesMux.Unlock()

		desc := peerConnection.RemoteDescription()
		if desc == nil {
			pendingCandidates = append(pendingCandidates, localIceCandidate)
		} else {
			sendLocalIceCandidate(wsConn, localIceCandidate)
		}
	})

	sendOffer(wsConn, peerConnection)
	startControlLoop(wsConn, peerConnection)

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
