package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"

	"cosmossdk.io/log"
	cmtcfg "github.com/cometbft/cometbft/config"
	"github.com/cometbft/cometbft/mempool"
	cmtp2p "github.com/cometbft/cometbft/p2p"
	pvm "github.com/cometbft/cometbft/privval"
	"github.com/cometbft/cometbft/proxy"
	"github.com/cometbft/cometbft/state/indexer"
	"github.com/cometbft/cometbft/state/indexer/block"
	"github.com/cometbft/cometbft/state/txindex"
	cmttypes "github.com/cometbft/cometbft/types"
	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/codec"
	"github.com/cosmos/cosmos-sdk/server"
	"github.com/cosmos/cosmos-sdk/server/api"
	serverconfig "github.com/cosmos/cosmos-sdk/server/config"
	servergrpc "github.com/cosmos/cosmos-sdk/server/grpc"
	servercmtlog "github.com/cosmos/cosmos-sdk/server/log"
	sdktypes "github.com/cosmos/cosmos-sdk/server/types"
	"github.com/cosmos/cosmos-sdk/telemetry"
	"github.com/cosmos/cosmos-sdk/version"
	genutiltypes "github.com/cosmos/cosmos-sdk/x/genutil/types"
	"github.com/hashicorp/go-metrics"
	ds "github.com/ipfs/go-datastore"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/rs/zerolog"
	"github.com/spf13/viper"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	evblock "github.com/evstack/ev-node/block"
	"github.com/evstack/ev-node/core/execution"
	coresequencer "github.com/evstack/ev-node/core/sequencer"
	"github.com/evstack/ev-node/node"
	"github.com/evstack/ev-node/pkg/config"
	"github.com/evstack/ev-node/pkg/da/jsonrpc"
	"github.com/evstack/ev-node/pkg/genesis"
	"github.com/evstack/ev-node/pkg/p2p"
	"github.com/evstack/ev-node/pkg/p2p/key"
	basedsequencer "github.com/evstack/ev-node/pkg/sequencers/based"
	singlesequencer "github.com/evstack/ev-node/pkg/sequencers/single"
	"github.com/evstack/ev-node/pkg/signer"
	"github.com/evstack/ev-node/pkg/store"
	rollkittypes "github.com/evstack/ev-node/types"

	"github.com/evstack/ev-abci/pkg/adapter"
	"github.com/evstack/ev-abci/pkg/rpc"
	"github.com/evstack/ev-abci/pkg/rpc/core"
	execsigner "github.com/evstack/ev-abci/pkg/signer"
	execstore "github.com/evstack/ev-abci/pkg/store"
)

const (
	flagTraceStore = "trace-store"
	flagGRPCOnly   = "grpc-only"
)

// StartCommandHandler is the type that must implement nova to match Cosmos SDK start logic.
type StartCommandHandler = func(
	svrCtx *server.Context,
	clientCtx client.Context,
	appCreator sdktypes.AppCreator,
	withCmt bool,
	opts server.StartCmdOptions,
) error

// StartHandler starts the Rollkit server with the provided application and options.
func StartHandler() StartCommandHandler {
	return func(svrCtx *server.Context, clientCtx client.Context, appCreator sdktypes.AppCreator, inProcess bool, opts server.StartCmdOptions) error {
		svrCfg, err := getAndValidateConfig(svrCtx)
		if err != nil {
			return err
		}

		app, appCleanupFn, err := startApp(svrCtx, appCreator, opts)
		if err != nil {
			return err
		}
		defer appCleanupFn()

		metrics, err := startTelemetry(svrCfg)
		if err != nil {
			return err
		}

		emitServerInfoMetrics()

		return startInProcess(svrCtx, svrCfg, clientCtx, app, metrics, opts)
	}
}

func startApp(svrCtx *server.Context, appCreator sdktypes.AppCreator, opts server.StartCmdOptions) (app sdktypes.Application, cleanupFn func(), err error) {
	traceWriter, traceCleanupFn, err := setupTraceWriter(svrCtx)
	if err != nil {
		return app, traceCleanupFn, err
	}

	home := svrCtx.Config.RootDir
	db, err := opts.DBOpener(home, server.GetAppDBBackend(svrCtx.Viper))
	if err != nil {
		return app, traceCleanupFn, err
	}

	app = appCreator(svrCtx.Logger, db, traceWriter, svrCtx.Viper)

	cleanupFn = func() {
		traceCleanupFn()
		if localErr := app.Close(); localErr != nil {
			svrCtx.Logger.Error(localErr.Error())
		}
	}
	return app, cleanupFn, nil
}

func startInProcess(svrCtx *server.Context, svrCfg serverconfig.Config, clientCtx client.Context, app sdktypes.Application,
	metrics *telemetry.Metrics, opts server.StartCmdOptions,
) error {
	cmtCfg := svrCtx.Config
	g, ctx, cancelFn := getCtx(svrCtx)
	defer cancelFn()

	if gRPCOnly := svrCtx.Viper.GetBool(flagGRPCOnly); gRPCOnly {
		// TODO: Generalize logic so that gRPC only is really in startStandAlone
		svrCtx.Logger.Info("starting node in gRPC only mode; CometBFT is disabled")
		svrCfg.GRPC.Enable = true
	} else {
		svrCtx.Logger.Info("starting node with ABCI CometBFT in-process")
		rollkitNode, executor, cleanupFn, err := setupNodeAndExecutor(ctx, svrCtx, cmtCfg, app)
		if err != nil {
			return err
		}
		defer cleanupFn()

		g.Go(func() error {
			svrCtx.Logger.Info("evolve node run loop launched in background goroutine")
			err := rollkitNode.Run(ctx)
			if err == context.Canceled {
				svrCtx.Logger.Info("evolve node run loop cancelled by context")
			} else if err != nil {
				return fmt.Errorf("evolve node run failed: %w", err)
			}

			// cancel context to stop all other processes
			cancelFn()

			return nil
		})

		if err := executor.Start(ctx); err != nil {
			return fmt.Errorf("failed to start executor: %w", err)
		}
		svrCtx.Logger.Info("executor started successfully")

		// Add the tx service to the gRPC router.
		if svrCfg.API.Enable || svrCfg.GRPC.Enable {
			// Use the started rpcServer for the client context
			// clientCtx = clientCtx.WithClient(rpcProvider)

			app.RegisterTxService(clientCtx)
			app.RegisterTendermintService(clientCtx)
			app.RegisterNodeService(clientCtx, svrCfg)
		}
	}

	// Start gRPC Server (if enabled)
	grpcSrv, clientCtx, err := startGrpcServer(ctx, g, svrCfg.GRPC, clientCtx, svrCtx, app)
	if err != nil {
		return err
	}

	// Start API Server (if enabled)
	err = startAPIServer(ctx, g, svrCfg, clientCtx, svrCtx, app, cmtCfg.RootDir, grpcSrv, metrics)
	if err != nil {
		return err
	}

	if opts.PostSetup != nil {
		if err := opts.PostSetup(svrCtx, clientCtx, ctx, g); err != nil {
			return err
		}
	}

	return g.Wait()
}

func startGrpcServer(
	ctx context.Context,
	g *errgroup.Group,
	config serverconfig.GRPCConfig,
	clientCtx client.Context,
	svrCtx *server.Context,
	app sdktypes.Application,
) (*grpc.Server, client.Context, error) {
	if !config.Enable {
		// return grpcServer as nil if gRPC is disabled
		return nil, clientCtx, nil
	}
	_, _, err := net.SplitHostPort(config.Address)
	if err != nil {
		return nil, clientCtx, err
	}

	maxSendMsgSize := config.MaxSendMsgSize
	if maxSendMsgSize == 0 {
		maxSendMsgSize = serverconfig.DefaultGRPCMaxSendMsgSize
	}

	maxRecvMsgSize := config.MaxRecvMsgSize
	if maxRecvMsgSize == 0 {
		maxRecvMsgSize = serverconfig.DefaultGRPCMaxRecvMsgSize
	}

	// if gRPC is enabled, configure gRPC client for gRPC gateway
	grpcClient, err := grpc.NewClient(
		config.Address,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(
			grpc.ForceCodec(codec.NewProtoCodec(clientCtx.InterfaceRegistry).GRPCCodec()),
			grpc.MaxCallRecvMsgSize(maxRecvMsgSize),
			grpc.MaxCallSendMsgSize(maxSendMsgSize),
		),
	)
	if err != nil {
		return nil, clientCtx, err
	}

	clientCtx = clientCtx.WithGRPCClient(grpcClient)
	svrCtx.Logger.Debug("gRPC client assigned to client context", "target", config.Address)

	grpcSrv, err := servergrpc.NewGRPCServer(clientCtx, app, config)
	if err != nil {
		return nil, clientCtx, err
	}

	// Start the gRPC server in a goroutine.
	// Note, the provided ctx will ensure that the server is gracefully shut down.
	g.Go(func() error {
		return servergrpc.StartGRPCServer(ctx, svrCtx.Logger.With("module", "grpc-server"), config, grpcSrv)
	})

	return grpcSrv, clientCtx, nil
}

func startAPIServer(
	ctx context.Context,
	g *errgroup.Group,
	svrCfg serverconfig.Config,
	clientCtx client.Context,
	svrCtx *server.Context,
	app sdktypes.Application,
	home string,
	grpcSrv *grpc.Server,
	metrics *telemetry.Metrics,
) error {
	if !svrCfg.API.Enable {
		return nil
	}

	clientCtx = clientCtx.WithHomeDir(home)

	apiSrv := api.New(clientCtx, svrCtx.Logger.With("module", "api-server"), grpcSrv)
	app.RegisterAPIRoutes(apiSrv, svrCfg.API)

	if svrCfg.Telemetry.Enabled {
		apiSrv.SetTelemetry(metrics)
	}

	g.Go(func() error {
		return apiSrv.Start(ctx, svrCfg)
	})

	return nil
}

func startTelemetry(cfg serverconfig.Config) (*telemetry.Metrics, error) {
	if !cfg.Telemetry.Enabled {
		return nil, nil
	}

	return telemetry.New(cfg.Telemetry)
}

func setupNodeAndExecutor(
	ctx context.Context,
	srvCtx *server.Context,
	cfg *cmtcfg.Config,
	app sdktypes.Application,
) (rolllkitNode node.Node, executor *adapter.Adapter, cleanupFn func(), err error) {
	sdkLogger := srvCtx.Logger.With("module", "evolve")
	sdkLogger.Info("starting node with ev-node in-process")

	evLogger, ok := sdkLogger.Impl().(*zerolog.Logger)
	if !ok {
		return nil, nil, cleanupFn, errors.New("failed to cast sdkLogger to zerolog.Logger")
	}

	pval := pvm.LoadOrGenFilePV(
		cfg.PrivValidatorKeyFile(),
		cfg.PrivValidatorStateFile(),
	)

	signingKey, err := execsigner.GetNodeKey(&cmtp2p.NodeKey{PrivKey: pval.Key.PrivKey})
	if err != nil {
		return nil, nil, cleanupFn, err
	}

	nodeKey := &key.NodeKey{PrivKey: signingKey, PubKey: signingKey.GetPublic()}

	// Map cosmos-sdk pruning keys to ev-node's config format.
	mapCosmosPruningToEvNode(srvCtx.Viper)

	evcfg, err := config.LoadFromViper(srvCtx.Viper)
	if err != nil {
		return nil, nil, cleanupFn, err
	}

	if err := evcfg.Validate(); err != nil {
		return nil, nil, cleanupFn, fmt.Errorf("failed to validate ev-node config: %w", err)
	}

	// only load signer if rollkit.node.aggregator == true
	var signer signer.Signer
	if evcfg.Node.Aggregator {
		signer, err = execsigner.NewSignerWrapper(pval.Key.PrivKey)
		if err != nil {
			return nil, nil, cleanupFn, err
		}
	}

	var (
		evGenesis     *genesis.Genesis
		appGenesis    *genutiltypes.AppGenesis
		daStartHeight uint64
	)

	// determine the genesis source: evolve genesis or app genesis
	migrationGenesis, err := loadEvolveMigrationGenesis(cfg.RootDir)
	if err != nil {
		return nil, nil, cleanupFn, err
	}

	if migrationGenesis != nil {
		evGenesis = migrationGenesis.ToEVGenesis()

		sdkLogger.Info("using evolve migration genesis",
			"chain_id", migrationGenesis.ChainID,
			"initial_height", migrationGenesis.InitialHeight)

		// this genesis is technically not needed, as abci exec won't run init chain again.
		appGenesis = &genutiltypes.AppGenesis{
			ChainID:       migrationGenesis.ChainID,
			InitialHeight: int64(migrationGenesis.InitialHeight),
			GenesisTime:   evGenesis.StartTime,
			Consensus: &genutiltypes.ConsensusGenesis{ // used in rpc/status.go
				Validators: []cmttypes.GenesisValidator{
					{
						Address: migrationGenesis.SequencerAddr,
						PubKey:  migrationGenesis.SequencerPubKey,
						Power:   1,
					},
				},
			},
		}
	} else {
		// normal scenario: create evolve genesis from full cometbft genesis
		appGenesis, err = getAppGenesis(cfg)
		if err != nil {
			return nil, nil, cleanupFn, err
		}

		daStartHeight, err = getDaStartHeight(cfg)
		if err != nil {
			return nil, nil, cleanupFn, err
		}

		daEpochSize, err := getDaEpoch(cfg)
		if err != nil {
			return nil, nil, cleanupFn, err
		}

		cmtGenDoc, err := appGenesis.ToGenesisDoc()
		if err != nil {
			return nil, nil, cleanupFn, err
		}

		evGenesis = createEvolveGenesisFromCometBFT(cmtGenDoc, daStartHeight, daEpochSize)

		sdkLogger.Info("created evolve genesis from cometbft genesis",
			"chain_id", cmtGenDoc.ChainID,
			"initial_height", cmtGenDoc.InitialHeight)
	}

	database, err := openRawEvolveDB(cfg.RootDir)
	if err != nil {
		return nil, nil, cleanupFn, err
	}

	metrics := node.DefaultMetricsProvider(evcfg.Instrumentation)

	_, p2pMetrics := metrics(evGenesis.ChainID)
	p2pLogger, ok := sdkLogger.With("module", "p2p").Impl().(*zerolog.Logger)
	if !ok {
		return nil, nil, cleanupFn, fmt.Errorf("failed to get p2p logger")
	}

	p2pClient, err := p2p.NewClient(evcfg.P2P, nodeKey.PrivKey, database, appGenesis.ChainID, *p2pLogger, p2pMetrics)
	if err != nil {
		return nil, nil, cleanupFn, err
	}

	var opts []adapter.Option
	if evcfg.Instrumentation.IsPrometheusEnabled() {
		m := adapter.PrometheusMetrics(config.DefaultInstrumentationConfig().Namespace, "chain_id", evGenesis.ChainID)
		opts = append(opts, adapter.WithMetrics(m))
	}

	executor = adapter.NewABCIExecutor(
		app,
		database,
		p2pClient,
		p2pMetrics,
		sdkLogger,
		cfg,
		appGenesis,
		opts...,
	)

	cmtApp := server.NewCometABCIWrapper(app)
	clientCreator := proxy.NewLocalClientCreator(cmtApp)

	proxyApp, err := initProxyApp(clientCreator, sdkLogger, proxy.NopMetrics())
	if err != nil {
		panic(err)
	}

	height, err := executor.RollkitStore.Height(context.Background())
	if err != nil {
		return nil, nil, cleanupFn, err
	}
	mempool := mempool.NewCListMempool(cfg.Mempool, proxyApp.Mempool(), int64(height))
	executor.SetMempool(mempool)

	// create the DA client
	daJsonRPC, err := jsonrpc.NewClient(
		ctx,
		evcfg.DA.Address,
		evcfg.DA.AuthToken,
		"",
	)
	if err != nil {
		return nil, nil, cleanupFn, fmt.Errorf("failed to create DA client: %w", err)
	}

	daClient := evblock.NewDAClient(daJsonRPC, evcfg, *evLogger)

	sequencer, err := createSequencer(*evLogger, database, evcfg, *evGenesis, daClient, executor)
	if err != nil {
		return nil, nil, cleanupFn, err
	}

	// Choose ValidatorHasherProvider based on attester mode (network soft confirmation)
	var validatorHasherProvider func(proposerAddress []byte, pubKey crypto.PubKey) (rollkittypes.Hash, error)
	if srvCtx.Viper.GetBool(FlagAttesterMode) {
		// Attester mode: use validators from ABCI store
		abciStore := execstore.NewExecABCIStore(database)
		validatorHasherProvider = adapter.ValidatorHasherFromStoreProvider(abciStore)
		sdkLogger.Info("using attester mode: validators will be read from ABCI store")
	} else {
		// Sequencer mode: single validator
		validatorHasherProvider = adapter.ValidatorHasherProvider()
		sdkLogger.Info("using sequencer mode: single validator")
	}

	rolllkitNode, err = node.NewNode(
		evcfg,
		executor,
		sequencer,
		daClient,
		signer,
		p2pClient,
		*evGenesis,
		database,
		metrics,
		*evLogger,
		node.NodeOptions{
			BlockOptions: evblock.BlockOptions{
				AggregatorNodeSignatureBytesProvider: adapter.AggregatorNodeSignatureBytesProvider(executor),
				SyncNodeSignatureBytesProvider:       adapter.SyncNodeSignatureBytesProvider(executor),
				ValidatorHasherProvider:              validatorHasherProvider,
			},
		},
	)
	if err != nil {
		return nil, nil, cleanupFn, err
	}
	eventBus, err := createAndStartEventBus(sdkLogger)
	if err != nil {
		return nil, nil, cleanupFn, fmt.Errorf("setup event-bus: %w", err)
	}
	executor.EventBus = eventBus

	idxSvc, txIndexer, blockIndexer, err := createAndStartIndexerService(cfg, evGenesis.ChainID, cmtcfg.DefaultDBProvider, eventBus, sdkLogger)
	if err != nil {
		return nil, nil, cleanupFn, fmt.Errorf("start indexer service: %w", err)
	}
	core.SetEnvironment(&core.Environment{
		Signer:       signer,
		Adapter:      executor,
		TxIndexer:    txIndexer,
		BlockIndexer: blockIndexer,
		Logger:       servercmtlog.CometLoggerWrapper{Logger: sdkLogger},
		RPCConfig:    *cfg.RPC,
		EVNodeConfig: evcfg,
		AttesterMode: srvCtx.Viper.GetBool(FlagAttesterMode),
	})

	// Pass the created handler to the RPC server constructor
	rpcServer := rpc.NewRPCServer(cfg.RPC, sdkLogger)
	if err = rpcServer.Start(); err != nil {
		return nil, nil, cleanupFn, fmt.Errorf("failed to start rpc server: %w", err)
	}

	cleanupFn = func() {
		if eventBus != nil {
			_ = eventBus.Stop()
		}
		if idxSvc != nil {
			_ = idxSvc.Stop()
		}
		if rpcServer != nil {
			_ = rpcServer.Stop()
		}
	}

	return rolllkitNode, executor, cleanupFn, nil
}

// createSequencer creates a sequencer based on the configuration.
// If BasedSequencer is enabled, it creates a based sequencer that fetches transactions from DA.
// Otherwise, it creates a single (traditional) sequencer.
func createSequencer(
	logger zerolog.Logger,
	datastore ds.Batching,
	nodeConfig config.Config,
	genesis genesis.Genesis,
	daClient evblock.FullDAClient,
	executor execution.Executor,
) (coresequencer.Sequencer, error) {
	if nodeConfig.Node.BasedSequencer {
		// Based sequencer mode - fetch transactions only from DA
		if !nodeConfig.Node.Aggregator {
			return nil, fmt.Errorf("based sequencer mode requires aggregator mode to be enabled")
		}

		basedSeq, err := basedsequencer.NewBasedSequencer(daClient, nodeConfig, datastore, genesis, logger, executor)
		if err != nil {
			return nil, fmt.Errorf("failed to create based sequencer: %w", err)
		}

		logger.Info().
			Str("forced_inclusion_namespace", nodeConfig.DA.GetForcedInclusionNamespace()).
			Uint64("da_epoch", genesis.DAEpochForcedInclusion).
			Msg("based sequencer initialized")

		return basedSeq, nil
	}

	sequencer, err := singlesequencer.NewSequencer(
		logger,
		datastore,
		daClient,
		nodeConfig,
		[]byte(genesis.ChainID),
		1000,
		genesis,
		executor,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create single sequencer: %w", err)
	}

	return sequencer, nil
}

func createAndStartEventBus(logger log.Logger) (*cmttypes.EventBus, error) {
	eventBus := cmttypes.NewEventBus()
	eventBus.SetLogger(servercmtlog.CometLoggerWrapper{Logger: logger}.With("module", "events"))
	if err := eventBus.Start(); err != nil {
		return nil, err
	}
	return eventBus, nil
}

func createAndStartIndexerService(
	config *cmtcfg.Config,
	chainID string,
	dbProvider cmtcfg.DBProvider,
	eventBus *cmttypes.EventBus,
	logger log.Logger,
) (*txindex.IndexerService, txindex.TxIndexer, indexer.BlockIndexer, error) {
	var (
		txIndexer    txindex.TxIndexer
		blockIndexer indexer.BlockIndexer
	)

	txIndexer, blockIndexer, allIndexersDisabled, err := block.IndexerFromConfigWithDisabledIndexers(config, dbProvider, chainID)
	if err != nil {
		return nil, nil, nil, err
	}
	if allIndexersDisabled {
		return nil, txIndexer, blockIndexer, nil
	}

	cmtLogger := servercmtlog.CometLoggerWrapper{Logger: logger}.With("module", "txindex")
	txIndexer.SetLogger(cmtLogger)
	blockIndexer.SetLogger(cmtLogger)

	indexerService := txindex.NewIndexerService(txIndexer, blockIndexer, eventBus, false)
	indexerService.SetLogger(cmtLogger)
	if err := indexerService.Start(); err != nil {
		return nil, nil, nil, err
	}

	return indexerService, txIndexer, blockIndexer, nil
}

func getAndValidateConfig(svrCtx *server.Context) (serverconfig.Config, error) {
	config, err := serverconfig.GetConfig(svrCtx.Viper)
	if err != nil {
		return config, err
	}

	if err := config.ValidateBasic(); err != nil {
		return config, err
	}
	return config, nil
}

func getCtx(svrCtx *server.Context) (*errgroup.Group, context.Context, context.CancelFunc) {
	ctx, cancelFn := context.WithCancel(context.Background())
	g, ctx := errgroup.WithContext(ctx)
	// listen for quit signals so the calling parent process can gracefully exit
	server.ListenForQuitSignals(g /* block */, false, cancelFn, svrCtx.Logger)
	return g, ctx, cancelFn
}

func openTraceWriter(traceWriterFile string) (w io.WriteCloser, err error) {
	if traceWriterFile == "" {
		return
	}
	return os.OpenFile( //nolint:gosec
		traceWriterFile,
		os.O_WRONLY|os.O_APPEND|os.O_CREATE,
		0o666,
	)
}

func setupTraceWriter(svrCtx *server.Context) (traceWriter io.WriteCloser, cleanup func(), err error) {
	// clean up the traceWriter when the server is shutting down
	cleanup = func() {}

	traceWriterFile := svrCtx.Viper.GetString(flagTraceStore)
	traceWriter, err = openTraceWriter(traceWriterFile)
	if err != nil {
		return traceWriter, cleanup, err
	}

	// if flagTraceStore is not used then traceWriter is nil
	if traceWriter != nil {
		cleanup = func() {
			if err = traceWriter.Close(); err != nil {
				svrCtx.Logger.Error("failed to close trace writer", "err", err)
			}
		}
	}

	return traceWriter, cleanup, nil
}

// emitServerInfoMetrics emits server info related metrics using application telemetry.
func emitServerInfoMetrics() {
	var ls []metrics.Label

	versionInfo := version.NewInfo()
	if len(versionInfo.GoVersion) > 0 {
		ls = append(ls, telemetry.NewLabel("go", versionInfo.GoVersion))
	}
	if len(versionInfo.CosmosSdkVersion) > 0 {
		ls = append(ls, telemetry.NewLabel("version", versionInfo.CosmosSdkVersion))
	}

	if len(ls) == 0 {
		return
	}

	telemetry.SetGaugeWithLabels([]string{"server", "info"}, 1, ls)
}

func initProxyApp(clientCreator proxy.ClientCreator, logger log.Logger, metrics *proxy.Metrics) (proxy.AppConns, error) {
	proxyApp := proxy.NewAppConns(clientCreator, metrics)
	proxyApp.SetLogger(servercmtlog.CometLoggerWrapper{Logger: logger}.With("module", "proxy"))
	if err := proxyApp.Start(); err != nil {
		return nil, fmt.Errorf("error while starting proxy app connections: %w", err)
	}
	return proxyApp, nil
}

func openRawEvolveDB(rootDir string) (ds.Batching, error) {
	database, err := store.NewDefaultKVStore(rootDir, "data", "evolve")
	if err != nil {
		return nil, err
	}

	return database, nil
}

// mapCosmosPruningToEvNode maps cosmos-sdk pruning configuration keys to ev-node format.
// Cosmos SDK uses: "pruning", "pruning-keep-recent", "pruning-interval"
// ev-node expects: "evnode.pruning.pruning_mode", "evnode.pruning.pruning_keep_recent", "evnode.pruning.pruning_interval"
func mapCosmosPruningToEvNode(v *viper.Viper) {
	// Get cosmos-sdk pruning configuration
	cosmosPruning := v.GetString("pruning")
	cosmosKeepRecent := v.GetString("pruning-keep-recent")
	cosmosInterval := v.GetString("pruning-interval")

	// Map cosmos-sdk pruning mode to ev-node pruning mode
	var evnodePruningMode string
	switch cosmosPruning {
	case "nothing":
		evnodePruningMode = "disabled"
	case "default", "everything", "custom":
		evnodePruningMode = "all"
	default:
		// If empty, don't set a mapping; unknown values default to "all"
		if cosmosPruning != "" {
			evnodePruningMode = "all"
		}
	}

	// Only set ev-node config if cosmos config was present
	if evnodePruningMode != "" {
		v.Set("evnode.pruning.pruning_mode", evnodePruningMode)
	}
	if cosmosKeepRecent != "" {
		v.Set("evnode.pruning.pruning_keep_recent", cosmosKeepRecent)
	}
	if cosmosInterval != "" {
		v.Set("evnode.pruning.pruning_interval", cosmosInterval)
	}
}
