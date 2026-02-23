package rpc

// Integration tests for WebSocket message type handling (issue #20, RFC 6455 §11.8).
//
// Investigation finding: CometBFT's RegisterRPCFuncs registers only HTTP and
// JSON-RPC handlers — it does NOT register a WebSocket endpoint. ev-abci now
// explicitly registers /websocket via WebsocketManager in startRPC.
//
// Binary frame behaviour: CometBFT's ws_handler calls NextReader() without
// distinguishing frame type. Binary payloads are fed to a JSON decoder; when
// the decode fails the server returns a parse-error response (code -32700)
// rather than crashing or silently dropping the frame.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"cosmossdk.io/log"
	cmtcfg "github.com/cometbft/cometbft/config"
	cmtlog "github.com/cometbft/cometbft/libs/log"
	rpcserver "github.com/cometbft/cometbft/rpc/jsonrpc/server"
	cmttypes "github.com/cometbft/cometbft/types"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"

	"github.com/evstack/ev-abci/pkg/adapter"
	"github.com/evstack/ev-abci/pkg/rpc/core"
)

// wsResponse mirrors the JSON-RPC response envelope sent by CometBFT's
// wsConnection over a WebSocket text frame.
type wsResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *wsRPCError     `json:"error,omitempty"`
}

type wsRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    string `json:"data,omitempty"`
}

// healthRequest is a minimal JSON-RPC health call. Health does not access the
// global env, so it is safe to use even with a minimal environment.
const healthRequest = `{"jsonrpc":"2.0","id":1,"method":"health","params":{}}`

// setupMinimalEnv configures the core.Environment global with enough state to
// prevent nil-pointer panics from other registered routes. Health itself does
// not access env at all.
func newTestEventBus(t *testing.T) *cmttypes.EventBus {
	t.Helper()
	eb := cmttypes.NewEventBus()
	eb.SetLogger(cmtlog.NewNopLogger())
	require.NoError(t, eb.Start())
	t.Cleanup(func() { _ = eb.Stop() })
	return eb
}

func setupMinimalEnv(t *testing.T) {
	t.Helper()
	core.SetEnvironment(&core.Environment{
		Adapter:   &adapter.Adapter{EventBus: newTestEventBus(t)},
		Logger:    cmtlog.NewNopLogger(),
		RPCConfig: *cmtcfg.DefaultRPCConfig(),
	})
}

// newWSServer creates an httptest.Server with the same HTTP + JSON-RPC +
// WebSocket routing as startRPC, allowing tests to supply custom WebsocketManager
// options (e.g. a read limit).
func newWSServer(t *testing.T, wm *rpcserver.WebsocketManager) (*httptest.Server, string) {
	t.Helper()
	logger := cmtlog.NewNopLogger()
	mux := http.NewServeMux()
	rpcserver.RegisterRPCFuncs(mux, core.Routes, logger)
	wm.SetLogger(logger)
	mux.HandleFunc("/websocket", wm.WebsocketHandler)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/websocket"
	return srv, wsURL
}

// dialWS dials wsURL and registers Close as a test cleanup.
func dialWS(t *testing.T, wsURL string) *websocket.Conn {
	t.Helper()
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// readWSResponse reads exactly one text frame and unmarshals it as a
// wsResponse. It fails the test if the read times out or the frame is not text.
func readWSResponse(t *testing.T, conn *websocket.Conn) wsResponse {
	t.Helper()
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(3*time.Second)))
	msgType, p, err := conn.ReadMessage()
	require.NoError(t, err)
	require.Equal(t, websocket.TextMessage, msgType, "server must respond with text frames")
	var resp wsResponse
	require.NoError(t, json.Unmarshal(p, &resp))
	return resp
}

// TestWebSocket_TextValidJSON sends a well-formed JSON-RPC health request as a
// WebSocket text frame and verifies a success response (no error field).
func TestWebSocket_TextValidJSON(t *testing.T) {
	setupMinimalEnv(t)
	_, wsURL := newWSServer(t, rpcserver.NewWebsocketManager(core.Routes))
	conn := dialWS(t, wsURL)

	require.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte(healthRequest)))

	resp := readWSResponse(t, conn)
	require.Nil(t, resp.Error, "expected no error, got: %+v", resp.Error)
	require.NotNil(t, resp.Result, "expected non-nil result for health")
}

// TestWebSocket_TextInvalidJSON sends malformed JSON as a text frame and
// verifies the server replies with a parse error (JSON-RPC code -32700) rather
// than crashing or closing the connection.
func TestWebSocket_TextInvalidJSON(t *testing.T) {
	setupMinimalEnv(t)
	_, wsURL := newWSServer(t, rpcserver.NewWebsocketManager(core.Routes))
	conn := dialWS(t, wsURL)

	require.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte("not valid json {{{{")))

	resp := readWSResponse(t, conn)
	require.NotNil(t, resp.Error, "expected parse error response")
	require.Equal(t, -32700, resp.Error.Code, "expected JSON-RPC parse error code -32700")
}

// TestWebSocket_BinaryFrame verifies that a binary WebSocket frame (RFC 6455
// type 0x2) is not silently dropped. CometBFT's wsConnection feeds the binary
// payload to a JSON decoder; when the decode fails the server responds with a
// parse error (code -32700) on a text frame, not a crash.
func TestWebSocket_BinaryFrame(t *testing.T) {
	setupMinimalEnv(t)
	_, wsURL := newWSServer(t, rpcserver.NewWebsocketManager(core.Routes))
	conn := dialWS(t, wsURL)

	require.NoError(t, conn.WriteMessage(websocket.BinaryMessage, []byte{0x00, 0x01, 0x02, 0x03}))

	resp := readWSResponse(t, conn)
	require.NotNil(t, resp.Error, "server must respond to binary frames, not drop them silently")
	require.Equal(t, -32700, resp.Error.Code, "binary frame should yield parse error code -32700")
}

// TestWebSocket_CloseFrame sends a normal close frame (code 1000) and verifies
// the server echoes it and terminates the connection cleanly.
func TestWebSocket_CloseFrame(t *testing.T) {
	setupMinimalEnv(t)
	_, wsURL := newWSServer(t, rpcserver.NewWebsocketManager(core.Routes))
	conn := dialWS(t, wsURL)

	closeMsg := websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")
	err := conn.WriteControl(websocket.CloseMessage, closeMsg, time.Now().Add(time.Second))
	require.NoError(t, err)

	require.NoError(t, conn.SetReadDeadline(time.Now().Add(3*time.Second)))
	_, _, err = conn.ReadMessage()
	require.Error(t, err, "connection must be closed after sending a close frame")
	require.True(t,
		websocket.IsCloseError(err, websocket.CloseNormalClosure),
		"expected CloseNormalClosure (1000), got: %v", err,
	)
}

// TestWebSocket_PingPong verifies that the server sends periodic ping control
// frames (RFC 6455 §5.5.2) and the client receives them within timeout.
//
// CometBFT's wsConnection.writeRoutine sends pings at the configured
// PingPeriod. The client's gorilla/websocket connection processes incoming
// pings in ReadMessage and invokes the registered PingHandler, which we
// override to detect receipt and send a pong back.
//
// Note on client-initiated pings: testing client→server pings is avoided here
// because CometBFT's ws_handler.go has a pre-existing data race — writeRoutine
// calls SetPingHandler concurrently with readRoutine's first advanceFrame call.
// That race is in upstream CometBFT code, outside ev-abci's scope.
// Server-initiated pings exercise an equivalent ping/pong code path.
func TestWebSocket_PingPong(t *testing.T) {
	setupMinimalEnv(t)
	const pingPeriod = 50 * time.Millisecond
	wm := rpcserver.NewWebsocketManager(
		core.Routes,
		rpcserver.ReadWait(5*time.Second),
		rpcserver.PingPeriod(pingPeriod),
	)
	_, wsURL := newWSServer(t, wm)
	conn := dialWS(t, wsURL)

	// Override the default ping handler to track receipt; still send pong so
	// the server resets its read deadline via its SetPongHandler.
	pingReceived := make(chan struct{}, 1)
	conn.SetPingHandler(func(data string) error {
		select {
		case pingReceived <- struct{}{}:
		default:
		}
		return conn.WriteControl(websocket.PongMessage, []byte(data), time.Now().Add(time.Second))
	})

	// Drive the client read loop so gorilla can process incoming control frames.
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(3*time.Second)))
	readDone := make(chan error, 1)
	go func() {
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				readDone <- err
				return
			}
		}
	}()

	select {
	case <-pingReceived:
		// Server-sent ping received; client responded with pong — verified.
	case err := <-readDone:
		t.Fatalf("read loop terminated before server ping arrived: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("server ping not received within 3 seconds")
	}
}

// TestWebSocket_LargeMessage verifies that when a message exceeds the
// configured WebSocket read limit, the server sends a CloseMessageTooBig frame
// (code 1009) and terminates the connection rather than crashing or hanging.
//
// By default, WebsocketManager sets no read limit (limit=0 means unlimited in
// gorilla/websocket). This test creates a server with a 64-byte read limit to
// exercise the limit-exceeded path.
func TestWebSocket_LargeMessage(t *testing.T) {
	setupMinimalEnv(t)
	const readLimit int64 = 64
	_, wsURL := newWSServer(t, rpcserver.NewWebsocketManager(core.Routes, rpcserver.ReadLimit(readLimit)))
	conn := dialWS(t, wsURL)

	largeMsg := bytes.Repeat([]byte("x"), int(readLimit)*4)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, largeMsg))

	require.NoError(t, conn.SetReadDeadline(time.Now().Add(3*time.Second)))
	_, _, err := conn.ReadMessage()
	require.Error(t, err, "connection must be closed after exceeding read limit")
	require.True(t,
		websocket.IsCloseError(err, websocket.CloseMessageTooBig),
		"expected CloseMessageTooBig (1009), got: %v", err,
	)
}

// TestRPCServer_WebSocketEndpointRegistered is an end-to-end integration test
// that starts a real RPCServer and confirms that /websocket is registered and
// accepts WebSocket connections, returning a valid JSON-RPC health response.
//
// Before the fix in server.go (issue #20), this test would fail with a 404/
// connection upgrade failure because RegisterRPCFuncs does not register a
// WebSocket endpoint.
func TestRPCServer_WebSocketEndpointRegistered(t *testing.T) {
	setupMinimalEnv(t)

	// Find a free port by binding and immediately releasing it.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := l.Addr().(*net.TCPAddr).Port
	require.NoError(t, l.Close())

	cfg := cmtcfg.DefaultRPCConfig()
	cfg.ListenAddress = fmt.Sprintf("tcp://127.0.0.1:%d", port)
	srv := NewRPCServer(cfg, log.NewNopLogger(), newTestEventBus(t))
	require.NoError(t, srv.Start())
	t.Cleanup(func() { _ = srv.Stop() })

	// Poll until the TCP port is ready to accept connections.
	require.Eventually(t, func() bool {
		c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 100*time.Millisecond)
		if err != nil {
			return false
		}
		_ = c.Close()
		return true
	}, 3*time.Second, 50*time.Millisecond, "RPC server did not start in time")

	wsURL := fmt.Sprintf("ws://127.0.0.1:%d/websocket", port)
	wsConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err, "/websocket endpoint must be registered on the RPC server")
	defer func() { _ = wsConn.Close() }()

	require.NoError(t, wsConn.WriteMessage(websocket.TextMessage, []byte(healthRequest)))

	require.NoError(t, wsConn.SetReadDeadline(time.Now().Add(3*time.Second)))
	_, msg, err := wsConn.ReadMessage()
	require.NoError(t, err)

	var resp wsResponse
	require.NoError(t, json.Unmarshal(msg, &resp))
	require.Nil(t, resp.Error, "health must succeed via RPCServer WebSocket")
	require.NotNil(t, resp.Result)
}
