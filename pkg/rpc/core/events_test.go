package core

import (
	"context"
	"strings"
	"testing"

	cmtcfg "github.com/cometbft/cometbft/config"
	cmtlog "github.com/cometbft/cometbft/libs/log"
	ctypes "github.com/cometbft/cometbft/rpc/core/types"
	rpctypes "github.com/cometbft/cometbft/rpc/jsonrpc/types"
	cmttypes "github.com/cometbft/cometbft/types"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/evstack/ev-abci/pkg/adapter"
)

// mockWSConn implements rpctypes.WSRPCConnection for testing.
// Subscribe starts a goroutine that writes events to the WSConn; mock
// allows those calls to succeed without blocking the test.
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

// newEventsTestEnv returns a base test Environment and its EventBus.
// The EventBus is created but not started; call startEventBus when the test
// requires actual subscriptions.
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

// startEventBus starts the EventBus and registers t.Cleanup to stop it.
func startEventBus(t *testing.T, eb *cmttypes.EventBus) {
	t.Helper()
	require.NoError(t, eb.Start())
	t.Cleanup(func() { _ = eb.Stop() })
}

// newWSCtx returns a rpctypes.Context backed by a mockWSConn and a valid
// JSONReq. Required for the valid Subscribe path, which captures
// ctx.JSONReq.ID before starting the event-forwarding goroutine.
func newWSCtx(t *testing.T) (*rpctypes.Context, *mockWSConn) {
	t.Helper()
	conn := new(mockWSConn)
	conn.On("WriteRPCResponse", mock.Anything, mock.Anything).Return(nil).Maybe()
	conn.On("TryWriteRPCResponse", mock.Anything).Return(true).Maybe()
	req := rpctypes.NewRPCRequest(rpctypes.JSONRPCStringID("test-id"), "subscribe", nil)
	return &rpctypes.Context{WSConn: conn, JSONReq: &req}, conn
}

// TestSubscribe verifies Subscribe behaviour across a range of inputs.
//
// CometBFT's JSON-RPC layer normalises the incoming param format (map vs
// array) before calling Subscribe, so by the time the function runs the
// query is always a plain string — see issue #17 and the CometBFT
// jsonParamsToArgs implementation.
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
			name: "empty query returns parse error",
			setup: func(t *testing.T) (*Environment, *rpctypes.Context) {
				e, _ := newEventsTestEnv(t)
				return e, &rpctypes.Context{}
			},
			query:   "",
			wantErr: "failed to parse query",
		},
		{
			name: "oversized query returns length error",
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

// TestUnsubscribe verifies Unsubscribe behaviour for valid and invalid inputs.
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
			}
		})
	}
}

// TestUnsubscribeAll verifies that UnsubscribeAll removes all subscriptions
// for the calling client.
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
	})
}
