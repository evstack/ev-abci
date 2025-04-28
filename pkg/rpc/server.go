package rpc

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"cosmossdk.io/log"
	cmtcfg "github.com/cometbft/cometbft/config"
	cmtlog "github.com/cometbft/cometbft/libs/log"
	servercmtlog "github.com/cosmos/cosmos-sdk/server/log"
	"github.com/rs/cors"
	"golang.org/x/net/netutil"

	"github.com/rollkit/go-execution-abci/pkg/rpc/json"
)

const (
	// maxQueryLength is the maximum length of a query string that will be
	// accepted. This is just a safety check to avoid outlandish queries.
	maxQueryLength = 512

	defaultPerPage = 30
	maxPerPage     = 100
)

var (
	// ErrConsensusStateNotAvailable moved to provider.go
	subscribeTimeout = 5 * time.Second
)

// NewRPCServer creates a new RPC server.
func NewRPCServer(
	provider *RpcProvider,
	cfg *cmtcfg.RPCConfig,
	logger log.Logger,
) *RPCServer {
	cmtLogger := servercmtlog.CometLoggerWrapper{Logger: logger}
	return &RPCServer{
		config:   cfg,
		provider: provider,
		logger:   cmtLogger,
	}
}

// RPCServer manages the HTTP server for RPC requests.
// It delegates the actual RPC method implementations to an rpcProvider.
type RPCServer struct {
	config   *cmtcfg.RPCConfig
	provider *RpcProvider
	server   http.Server
	logger   cmtlog.Logger
}

// GetProvider returns the underlying rpcProvider which implements the CometRPC interface.
func (r *RPCServer) GetProvider() *RpcProvider {
	return r.provider
}

// Start starts the RPC server.
func (r *RPCServer) Start() error {
	return r.startRPC()
}

func (r *RPCServer) startRPC() error {
	if r.config.ListenAddress == "" {
		r.logger.Info("Listen address not specified - RPC will not be exposed")
		return nil
	}
	parts := strings.SplitN(r.config.ListenAddress, "://", 2)
	if len(parts) != 2 {
		return errors.New("invalid RPC listen address: expecting tcp://host:port")
	}
	proto := parts[0]
	addr := parts[1]

	listener, err := net.Listen(proto, addr)
	if err != nil {
		return err
	}

	if r.config.MaxOpenConnections != 0 {
		r.logger.Debug("limiting number of connections", "limit", r.config.MaxOpenConnections)
		listener = netutil.LimitListener(listener, r.config.MaxOpenConnections)
	}

	handler, err := json.GetHTTPHandler(r.provider, r.provider.logger)
	if err != nil {
		return err
	}

	if r.config.IsCorsEnabled() {
		r.logger.Debug("CORS enabled",
			"origins", r.config.CORSAllowedOrigins,
			"methods", r.config.CORSAllowedMethods,
			"headers", r.config.CORSAllowedHeaders,
		)
		c := cors.New(cors.Options{
			AllowedOrigins: r.config.CORSAllowedOrigins,
			AllowedMethods: r.config.CORSAllowedMethods,
			AllowedHeaders: r.config.CORSAllowedHeaders,
		})
		handler = c.Handler(handler)
	}

	go func() {
		err := r.serve(listener, handler)
		if !errors.Is(err, http.ErrServerClosed) {
			r.logger.Error("error while serving HTTP", "error", err)
		}
	}()

	return nil
}

func (r *RPCServer) serve(listener net.Listener, handler http.Handler) error {
	r.logger.Info("serving HTTP", "listen address", listener.Addr())
	r.server = http.Server{
		Handler:           handler,
		ReadHeaderTimeout: time.Second * 2,
	}
	if r.config.TLSCertFile != "" && r.config.TLSKeyFile != "" {
		return r.server.ServeTLS(listener, r.config.CertFile(), r.config.KeyFile())
	}
	return r.server.Serve(listener)
}

// Stop stops the RPC server gracefully.
func (r *RPCServer) Stop() error {
	r.logger.Info("Stopping RPC server")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := r.server.Shutdown(ctx); err != nil {
		r.logger.Error("RPC server shutdown error", "err", err)
		return err
	}
	r.logger.Info("RPC server stopped")
	return nil
}
