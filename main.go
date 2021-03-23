// This program forwards WebRTC streams from Unreal Engine pixel streaming over RTP to some arbitrary receiever.
// This program uses websockets to connect to Unreal Engine pixel streaming through the intermediate signalling server ("cirrus").
// This program then uses Pion WebRTC to receive video/audio from Unreal Engine and the forwards those RTP streams
// to a specified address and ports. This is a proof of concept that is designed so FFPlay can receive these RTP streams.
// This program is a heavily modified version of: https://github.com/pion/webrtc/tree/master/examples/rtp-forwarder

package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/url"
	"time"
	"flag"

	"github.com/gorilla/websocket"
	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v3"
)

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

// RTPVideoPayloadType - The payload type of the RTP packet, 102 is H264.
var RTPVideoPayloadType = flag.Uint("RTPVideoPayloadType", 102, "The payload type of the RTP packet, 102 is H264.")

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
		return "", err
	}

	if err = peerConnection.SetLocalDescription(offer); err != nil {
		log.Println("Error setting local description of peer connection: ", err)
		return "", err
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
		PayloadType:        webrtc.PayloadType(*RTPVideoPayloadType),
	}, webrtc.RTPCodecTypeVideo); err != nil {
		return nil, err
	}
	if err := m.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{MimeType: "audio/opus", ClockRate: 48000, Channels: 2, SDPFmtpLine: "111 minptime=10;useinbandfec=1", RTCPFeedback: nil},
		PayloadType:        webrtc.PayloadType(*RTPAudioPayloadType),
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
func handleRemoteAnswer(message []byte, peerConnection *webrtc.PeerConnection, wsConn *websocket.Conn, pendingCandidates *[]*webrtc.ICECandidate) {
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
	fmt.Println("Added session description from UE to Pion.")

	// User websocket to send our local ICE candidates to UE
	for _, localIceCandidate := range *pendingCandidates {
		sendLocalIceCandidate(wsConn, localIceCandidate)
	}
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
func startControlLoop(wsConn *websocket.Conn, peerConnection *webrtc.PeerConnection, pendingCandidates *[]*webrtc.ICECandidate) {
	// Start loop here to read web socket messages
	for {

		messageType, message, err := wsConn.ReadMessage()
		if err != nil {
			log.Printf("Websocket read message error: %v", err)
			log.Printf("Closing Pion websocket control loop.")
			wsConn.Close()
			break
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
			handleRemoteAnswer(message, peerConnection, wsConn, pendingCandidates)
		case "iceCandidate":
			candidateMsg := objmap["candidate"]
			handleRemoteIceCandidate(candidateMsg, peerConnection)
		default:
			log.Println("Got message we do not specifically handle, type was: " + pixelStreamingMessageType)
		}

	}
}

// Send an "offer" string over websocket to Unreal Engine to start the WebRTC handshake.
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
	fmt.Println(fmt.Sprintf("Sending our local ice candidate to UE...%s", jsonStr))
}

func createUDPConnection(address string, port int, payloadType uint8) (*udpConn, error) {

	var udpConnection udpConn = udpConn{port: port, payloadType: payloadType}

	// Create remote addr
	var raddr *net.UDPAddr
	var resolveRemoteErr error
	if raddr, resolveRemoteErr = net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", address, port)); resolveRemoteErr != nil {
		return nil, resolveRemoteErr
	}

	// Dial udp
	var udpConnErr error
	if udpConnection.conn, udpConnErr = net.DialUDP("udp", nil, raddr); udpConnErr != nil {
		return nil, udpConnErr
	}
	return &udpConnection, nil
}

func setupMediaForwarding(peerConnection *webrtc.PeerConnection) (*udpConn, *udpConn) {

	// Prepare udp conns
	// Also update incoming packets with expected PayloadType, the browser may use
	// a different value. We have to modify so our stream matches what rtp-forwarder.sdp expects
	videoUDPConn, err := createUDPConnection(*ForwardingAddress, *RTPVideoForwardingPort, uint8(*RTPVideoPayloadType))

	if err != nil {
		log.Println(fmt.Sprintf("Error creating udp connection for video: " + err.Error()))
	}

	audioUDPConn, err := createUDPConnection(*ForwardingAddress, *RTPAudioForwardingPort, uint8(*RTPAudioPayloadType))

	if err != nil {
		log.Println(fmt.Sprintf("Error creating udp connection for audio: " + err.Error()))
	}

	peerConnection.OnTrack(func(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {

		var trackType string = track.Kind().String()
		fmt.Println(fmt.Sprintf("Got %s track from Unreal Engine Pixel Streaming WebRTC.", trackType))

		var udpConnection *udpConn
		switch trackType {
		case "audio":
			udpConnection = audioUDPConn
		case "video":
			udpConnection = videoUDPConn
		default:
			log.Println(fmt.Sprintf("Unsupported track type from Unreal Engine, track type: %s", trackType))
		}

		// Send a PLI on an interval so that the publisher is pushing a keyframe every rtcpPLIInterval
		go func() {
			ticker := time.NewTicker(time.Second * 2)
			for range ticker.C {
				if rtcpErr := peerConnection.WriteRTCP([]rtcp.Packet{&rtcp.PictureLossIndication{MediaSSRC: uint32(track.SSRC())}}); rtcpErr != nil {
					fmt.Println(rtcpErr)
				}
			}
		}()

		b := make([]byte, 1500)
		rtpPacket := &rtp.Packet{}
		for {
			// Read
			n, _, readErr := track.Read(b)
			if readErr != nil {
				panic(readErr)
			}

			// Unmarshal the packet and update the PayloadType
			if err = rtpPacket.Unmarshal(b[:n]); err != nil {
				panic(err)
			}
			rtpPacket.PayloadType = udpConnection.payloadType

			// Marshal into original buffer with updated PayloadType
			if n, err = rtpPacket.MarshalTo(b); err != nil {
				panic(err)
			}

			// Write
			if _, err = udpConnection.conn.Write(b[:n]); err != nil {
				// For this particular example, third party applications usually timeout after a short
				// amount of time during which the user doesn't have enough time to provide the answer
				// to the browser.
				// That's why, for this particular example, the user first needs to provide the answer
				// to the browser then open the third party application. Therefore we must not kill
				// the forward on "connection refused" errors
				if opError, ok := err.(*net.OpError); ok && opError.Err.Error() == "write: connection refused" {
					continue
				}
				panic(err)
			}
		}

	})

	return videoUDPConn, audioUDPConn
}

func main() {
	flag.Parse()

	// Setup a websocket connection between this application and the Cirrus webserver.
	serverURL := url.URL{Scheme: "ws", Host: fmt.Sprintf("%s:%d", *CirrusAddress, *CirrusPort), Path: "/"}
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
	pendingCandidates := make([]*webrtc.ICECandidate, 0)

	// Setup a callback to capture our local ice candidates when they are ready
	// Note: can happen at random times so might be before or after we have sent offer.
	peerConnection.OnICECandidate(func(localIceCandidate *webrtc.ICECandidate) {
		if localIceCandidate == nil {
			return
		}

		desc := peerConnection.RemoteDescription()
		if desc == nil {
			pendingCandidates = append(pendingCandidates, localIceCandidate)
			fmt.Println("Added local ICE candidate that we will send off later...")
		} else {
			sendLocalIceCandidate(wsConn, localIceCandidate)
		}
	})

	// Set the handler for ICE connection state
	// This will notify you when the peer has connected/disconnected
	peerConnection.OnICEConnectionStateChange(func(connectionState webrtc.ICEConnectionState) {

		colorPurple := "\033[35m"
		colorReset := "\033[0m"

		fmt.Printf("Connection State has changed %s \n", connectionState.String())

		if connectionState == webrtc.ICEConnectionStateConnected {
			fmt.Println(string(colorPurple), "Connected to UE Pixel Streaming!", string(colorReset))
		} else if connectionState == webrtc.ICEConnectionStateFailed || connectionState == webrtc.ICEConnectionStateDisconnected {
			fmt.Println(string(colorPurple), "Disconnected from UE Pixel Streaming.", string(colorReset))
		}
	})

	videoUDP, audioUDP := setupMediaForwarding(peerConnection)
	defer videoUDP.conn.Close()
	defer audioUDP.conn.Close()

	sendOffer(wsConn, peerConnection)
	startControlLoop(wsConn, peerConnection, &pendingCandidates)

}
