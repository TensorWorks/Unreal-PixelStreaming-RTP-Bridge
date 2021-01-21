
// JS in browser
//------------------------------

//setup websocket in browser
ws = new WebSocket(window.location.href.replace('http://', 'ws://').replace('https://', 'wss://'));

// setup an "offer" using web browser APIs RTCPeerConnection
pc = new RTCPeerConnection(self.cfg);

//peer connection create offer
pc.createOffer(self.sdpConstraints)

//send the offer string using websockets (goes from browser back to server this way)
ws.send(offerStr); 


//---------------------------------------------------------------------------------------



// NodeJS Cirrus 
//------------------------------
//Server has two websocket servers: 1) to Unreal Engine 2) to the browser

//web browser websocket server
let playerServer = new WebSocket.Server({ server: config.UseHTTPS ? https : http});

//streamer (aka Unreal Engine) websocket server
var streamerPort = 8888;
let streamerServer = new WebSocket.Server({ port: streamerPort, backlog: 1 });
var streamer;
streamerServer.on('connection', function (ws, req) {
	streamer = ws
}

msg.playerId = playerId;
streamer.send(JSON.stringify(msg));



//TODO
//In Golang, implement websocket messages to ws://localhost
// see: https://github.com/pion/example-webrtc-applications/blob/master/sfu-ws/main.go
// see: https://github.com/gorilla/websocket

// 'offer' - the sdp offer in plaintext
// 'iceCandidate' - JSON.stringify({ type: 'iceCandidate', candidate: candidate })
