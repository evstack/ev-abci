package core

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	cmtcfg "github.com/cometbft/cometbft/config"
	cmtlog "github.com/cometbft/cometbft/libs/log"
	cmtpubsub "github.com/cometbft/cometbft/libs/pubsub"
	ctypes "github.com/cometbft/cometbft/rpc/core/types"
	rpctypes "github.com/cometbft/cometbft/rpc/jsonrpc/types"
	cmttypes "github.com/cometbft/cometbft/types"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/evstack/ev-abci/pkg/adapter"
)

// mockWSConn implements rpctypes.WSRPCConnection for testing.
// Subscribe starts a goroutine that writes events to the WSConn; using a
// mock allows tests to capture and assert those calls.
type mockWSConn struct {
	mock.Mock
}

func (m *mockWSConn) GetRemoteAddr() string { return "127.0.0.1:0" }

func (m *mockWSConn) WriteRPCResponse(ctx context.Context, resp rpctypes.RPCResponse) error {
	args := m.Called(ctx, resp)
	return args.Error(0)
}

func (m *mockWSConn) TryWriteRPCResponse(resp rpctypes.RPCResponse) bool {
	args := m.Called(resp)
	return args.Bool(0)
}

func (m *mockWSConn) Context() context.Context { return context.Background() }

// newEventsTestEnv creates a base test Environment with an unstarted EventBus.
// Call startEventBus separately when a test requires actual subscriptions.
func newEventsTestEnv(t *testing.T) (*Environment, *cmttypes.EventBus) {
	t.Helper()
	eventBus := cmttypes.NewEventBus()
	eventBus.SetLogger(cmtlog.NewNopLogger())
	rpcCfg := *cmtcfg.DefaultRPCConfig()
	e := &Environment{
		Adapter:   &adapter.Adapter{EventBus: eventBus},
		Logger:    cmtlog.NewNopLogger(),
		RPCConfig: rpcCfg,
	}
	return e, eventBus
}

// startEventBus starts the EventBus and registers a Cleanup to stop it.
func startEventBus(t *testing.T, eb *cmttypes.EventBus) {
	t.Helper()
	require.NoError(t, eb.Start())
	t.Cleanup(func() { _ = eb.Stop() })
}

// newWSCtx returns a rpctypes.Context with a permissive mock WSConn (all
// calls accepted but not asserted). Use for tests that do not exercise the
// event-forwarding goroutine.
func newWSCtx(t *testing.T) (*rpctypes.Context, *mockWSConn) {
	t.Helper()
	conn := new(mockWSConn)
	conn.On("WriteRPCResponse", mock.Anything, mock.Anything).Return(nil).Maybe()
	conn.On("TryWriteRPCResponse", mock.Anything).Return(true).Maybe()
	req := rpctypes.NewRPCRequest(rpctypes.JSONRPCStringID("test-id"), "subscribe", nil)
	return &rpctypes.Context{WSConn: conn, JSONReq: &req}, conn
}

// TestSubscribe verifies Subscribe input validation and basic return value.
//
// CometBFT's JSON-RPC layer normalises incoming subscribe parameters before
// this function is called via jsonParamsToArgs in
// rpc/jsonrpc/server/http_json_handler.go. Both the legacy Tendermint map
// format and the array format are transparently converted to a plain string,
// so query is always a plain Go string here — see issue #17.
func TestSubscribe(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(t *testing.T) (*Environment, *rpctypes.Context)
		query   string
		wantErr string // substring; empty means no error expected
	}{
		{
			name: "valid query returns ResultSubscribe",
			setup: func(t *testing.T) (*Environment, *rpctypes.Context) {
				e, eb := newEventsTestEnv(t)
				startEventBus(t, eb)
				ctx, _ := newWSCtx(t)
				return e, ctx
			},
			query: "tm.event='NewBlock'",
		},
		{
			name: "query at max length (512) succeeds",
			setup: func(t *testing.T) (*Environment, *rpctypes.Context) {
				e, eb := newEventsTestEnv(t)
				startEventBus(t, eb)
				ctx, _ := newWSCtx(t)
				return e, ctx
			},
			// maxQueryLength == 512; condition is len > 512, so exactly 512 must succeed.
			// Use a valid query padded to exactly 512 chars.
			query: "tm.event='NewBlock' AND abcdefgh='" + strings.Repeat("x", 512-len("tm.event='NewBlock' AND abcdefgh=''")) + "'",
		},
		{
			name: "empty query returns parse error",
			setup: func(t *testing.T) (*Environment, *rpctypes.Context) {
				e, _ := newEventsTestEnv(t)
				return e, &rpctypes.Context{}
			},
			query:   "",
			wantErr: "failed to parse query",
		},
		{
			name: "oversized query (513 chars) returns length error",
			setup: func(t *testing.T) (*Environment, *rpctypes.Context) {
				e, _ := newEventsTestEnv(t)
				return e, &rpctypes.Context{}
			},
			query:   strings.Repeat("x", maxQueryLength+1),
			wantErr: "maximum query length exceeded",
		},
		{
			name: "malformed query returns parse error",
			setup: func(t *testing.T) (*Environment, *rpctypes.Context) {
				e, _ := newEventsTestEnv(t)
				return e, &rpctypes.Context{}
			},
			query:   "!!!invalid",
			wantErr: "failed to parse query",
		},
		{
			name: "max subscription clients exceeded",
			setup: func(t *testing.T) (*Environment, *rpctypes.Context) {
				e, _ := newEventsTestEnv(t)
				e.RPCConfig.MaxSubscriptionClients = 0
				return e, &rpctypes.Context{}
			},
			query:   "tm.event='NewBlock'",
			wantErr: "max_subscription_clients 0 reached",
		},
		{
			name: "max subscriptions per client exceeded",
			setup: func(t *testing.T) (*Environment, *rpctypes.Context) {
				e, _ := newEventsTestEnv(t)
				e.RPCConfig.MaxSubscriptionsPerClient = 0
				return e, &rpctypes.Context{}
			},
			query:   "tm.event='NewBlock'",
			wantErr: "max_subscriptions_per_client 0 reached",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e, ctx := tc.setup(t)
			env = e

			result, err := Subscribe(ctx, tc.query)

			if tc.wantErr != "" {
				require.Error(t, err)
				require.ErrorContains(t, err, tc.wantErr)
				require.Nil(t, result)
			} else {
				require.NoError(t, err)
				require.Equal(t, &ctypes.ResultSubscribe{}, result)
			}
		})
	}
}

// TestSubscribe_EventDelivery verifies that the goroutine started by Subscribe
// actually forwards published events to the WebSocket connection via
// WriteRPCResponse.
func TestSubscribe_EventDelivery(t *testing.T) {
	const query = "tm.event='NewBlock'"
	const subID = rpctypes.JSONRPCStringID("delivery-sub")

	e, eb := newEventsTestEnv(t)
	startEventBus(t, eb)

	conn := new(mockWSConn)
	// Capture the response when WriteRPCResponse is called by the goroutine.
	deliverCh := make(chan rpctypes.RPCResponse, 1)
	conn.On("WriteRPCResponse", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			deliverCh <- args.Get(1).(rpctypes.RPCResponse)
		}).
		Return(nil).Once()
	// Allow TryWriteRPCResponse during EventBus cleanup (bus Stop cancels sub).
	conn.On("TryWriteRPCResponse", mock.Anything).Return(true).Maybe()

	req := rpctypes.NewRPCRequest(subID, "subscribe", nil)
	ctx := &rpctypes.Context{WSConn: conn, JSONReq: &req}

	env = e
	_, err := Subscribe(ctx, query)
	require.NoError(t, err)

	// Publish a matching event. The goroutine should pick it up and call
	// WriteRPCResponse with a ResultEvent containing the query.
	require.NoError(t, eb.PublishEventNewBlock(cmttypes.EventDataNewBlock{}))

	select {
	case resp := <-deliverCh:
		require.Nil(t, resp.Error, "expected success response, got error: %v", resp.Error)
		require.Equal(t, subID, resp.ID)
		// Decode just the query field to confirm it is the correct ResultEvent.
		var result struct {
			Query string `json:"query"`
		}
		require.NoError(t, json.Unmarshal(resp.Result, &result))
		require.Equal(t, query, result.Query)
	case <-time.After(3 * time.Second):
		t.Fatal("timeout: WriteRPCResponse not called within 3s after event publish")
	}

	conn.AssertExpectations(t)
}

// TestSubscribe_SlowClient verifies that when WriteRPCResponse returns an error
// and CloseOnSlowClient is true, the goroutine sends a cancellation error via
// TryWriteRPCResponse and terminates.
func TestSubscribe_SlowClient(t *testing.T) {
	const subID = rpctypes.JSONRPCStringID("slow-client-sub")

	e, eb := newEventsTestEnv(t)
	e.RPCConfig.CloseOnSlowClient = true
	startEventBus(t, eb)

	conn := new(mockWSConn)
	// WriteRPCResponse returns an error to simulate a slow/unresponsive client.
	conn.On("WriteRPCResponse", mock.Anything, mock.Anything).
		Return(errors.New("write: connection reset")).Once()

	// The goroutine must follow up with TryWriteRPCResponse carrying the
	// cancellation reason.
	tryWriteCh := make(chan rpctypes.RPCResponse, 1)
	conn.On("TryWriteRPCResponse", mock.Anything).
		Run(func(args mock.Arguments) {
			tryWriteCh <- args.Get(0).(rpctypes.RPCResponse)
		}).
		Return(true).Once()

	req := rpctypes.NewRPCRequest(subID, "subscribe", nil)
	ctx := &rpctypes.Context{WSConn: conn, JSONReq: &req}

	env = e
	_, err := Subscribe(ctx, "tm.event='NewBlock'")
	require.NoError(t, err)

	require.NoError(t, eb.PublishEventNewBlock(cmttypes.EventDataNewBlock{}))

	select {
	case resp := <-tryWriteCh:
		require.NotNil(t, resp.Error)
		require.Equal(t, subID, resp.ID)
		require.Contains(t, resp.Error.Data, "slow client",
			"expected cancellation message to mention slow client, got: %q", resp.Error.Data)
	case <-time.After(3 * time.Second):
		t.Fatal("timeout: TryWriteRPCResponse not called within 3s")
	}

	conn.AssertExpectations(t)
}

// TestSubscribe_Cancellation verifies that when the EventBus is stopped, the
// goroutine receives the Canceled() signal and sends a "CometBFT exited"
// error via TryWriteRPCResponse.
func TestSubscribe_Cancellation(t *testing.T) {
	const subID = rpctypes.JSONRPCStringID("cancel-sub")

	e, eb := newEventsTestEnv(t)
	// Start manually so we can stop it during the test. startEventBus would
	// register a Cleanup that double-stops, which is safe (returns
	// ErrAlreadyStopped) but cleaner to avoid.
	require.NoError(t, eb.Start())

	conn := new(mockWSConn)
	tryWriteCh := make(chan rpctypes.RPCResponse, 1)
	conn.On("TryWriteRPCResponse", mock.Anything).
		Run(func(args mock.Arguments) {
			tryWriteCh <- args.Get(0).(rpctypes.RPCResponse)
		}).
		Return(true).Once()

	req := rpctypes.NewRPCRequest(subID, "subscribe", nil)
	ctx := &rpctypes.Context{WSConn: conn, JSONReq: &req}

	env = e
	_, err := Subscribe(ctx, "tm.event='NewBlock'")
	require.NoError(t, err)

	// Stopping the bus triggers removeAll(nil) in the pubsub, which calls
	// sub.cancel(nil). The goroutine's case <-sub.Canceled() fires with
	// sub.Err() == nil, so it sends reason = "CometBFT exited".
	require.NoError(t, eb.Stop())

	select {
	case resp := <-tryWriteCh:
		require.NotNil(t, resp.Error)
		require.Equal(t, subID, resp.ID)
		require.Contains(t, resp.Error.Data, "CometBFT exited",
			"expected cancellation reason 'CometBFT exited', got: %q", resp.Error.Data)
	case <-time.After(3 * time.Second):
		t.Fatal("timeout: TryWriteRPCResponse not called within 3s after bus shutdown")
	}

	conn.AssertExpectations(t)
}

// TestUnsubscribe verifies Unsubscribe behaviour.
func TestUnsubscribe(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(t *testing.T) (*Environment, *rpctypes.Context)
		query   string
		wantErr string
	}{
		{
			name: "valid unsubscribe after subscribe",
			setup: func(t *testing.T) (*Environment, *rpctypes.Context) {
				e, eb := newEventsTestEnv(t)
				startEventBus(t, eb)
				ctx, _ := newWSCtx(t)
				// Subscribe first so the client has an active subscription.
				env = e
				_, err := Subscribe(ctx, "tm.event='NewBlock'")
				require.NoError(t, err)
				return e, ctx
			},
			query: "tm.event='NewBlock'",
		},
		{
			name: "unsubscribe without prior subscribe returns not-found error",
			setup: func(t *testing.T) (*Environment, *rpctypes.Context) {
				e, eb := newEventsTestEnv(t)
				startEventBus(t, eb)
				return e, &rpctypes.Context{}
			},
			query:   "tm.event='NewBlock'",
			wantErr: cmtpubsub.ErrSubscriptionNotFound.Error(),
		},
		{
			name: "malformed query returns parse error",
			setup: func(t *testing.T) (*Environment, *rpctypes.Context) {
				e, _ := newEventsTestEnv(t)
				return e, &rpctypes.Context{}
			},
			query:   "!!!invalid",
			wantErr: "failed to parse query",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e, ctx := tc.setup(t)
			env = e

			result, err := Unsubscribe(ctx, tc.query)

			if tc.wantErr != "" {
				require.Error(t, err)
				require.ErrorContains(t, err, tc.wantErr)
				require.Nil(t, result)
			} else {
				require.NoError(t, err)
				require.Equal(t, &ctypes.ResultUnsubscribe{}, result)
				// Verify the subscription was actually removed from the bus.
				require.Equal(t, 0, e.Adapter.EventBus.NumClientSubscriptions(ctx.RemoteAddr()))
			}
		})
	}
}

// TestUnsubscribeAll verifies UnsubscribeAll removes all client subscriptions.
func TestUnsubscribeAll(t *testing.T) {
	t.Run("unsubscribes all active subscriptions", func(t *testing.T) {
		e, eb := newEventsTestEnv(t)
		startEventBus(t, eb)
		ctx, _ := newWSCtx(t)

		env = e
		_, err := Subscribe(ctx, "tm.event='NewBlock'")
		require.NoError(t, err)
		_, err = Subscribe(ctx, "tm.event='Tx'")
		require.NoError(t, err)

		result, err := UnsubscribeAll(ctx)
		require.NoError(t, err)
		require.Equal(t, &ctypes.ResultUnsubscribe{}, result)
		require.Equal(t, 0, e.Adapter.EventBus.NumClientSubscriptions(ctx.RemoteAddr()))
	})

	t.Run("no active subscriptions returns not-found error", func(t *testing.T) {
		e, eb := newEventsTestEnv(t)
		startEventBus(t, eb)

		env = e
		result, err := UnsubscribeAll(&rpctypes.Context{})
		require.Error(t, err)
		require.ErrorIs(t, err, cmtpubsub.ErrSubscriptionNotFound)
		require.Nil(t, result)
	})
}
