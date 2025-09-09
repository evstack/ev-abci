package server

import (
	"context"
	"fmt"
	"path/filepath"

	goheaderstore "github.com/celestiaorg/go-header/store"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/cosmos/cosmos-sdk/server"
	"github.com/cosmos/cosmos-sdk/server/types"
	ds "github.com/ipfs/go-datastore"
	kt "github.com/ipfs/go-datastore/keytransform"
	"github.com/spf13/cobra"

	"github.com/evstack/ev-node/node"
	"github.com/evstack/ev-node/pkg/store"
	evtypes "github.com/evstack/ev-node/types"
)

// NewRollbackCmd creates a command to rollback CometBFT and multistore state by one height.
func NewRollbackCmd(appCreator types.AppCreator, defaultNodeHome string) *cobra.Command {
	var height uint64

	openDB := func(rootDir string, backendType dbm.BackendType) (dbm.DB, error) {
		dataDir := filepath.Join(rootDir, "data")
		return dbm.NewDB("application", backendType, dataDir)
	}

	cmd := &cobra.Command{
		Use:   "rollback",
		Short: "rollback Cosmos SDK and EV-node state by one height",
		Long: `
A state rollback is performed to recover from an incorrect application state transition,
when EV-node has persisted an incorrect app hash or incorrect block data.
Rollback overwrites a state at height n with the state at height n - 1.
The application also rolls back to height n - 1. If a --height flag is specified, the rollback will be performed to that height. No rollback can be performed if the height has been committed to the DA layer.
`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := server.GetServerContextFromCmd(cmd)
			cfg := ctx.Config
			home := cfg.RootDir

			goCtx := cmd.Context()
			if goCtx == nil {
				goCtx = context.Background()
			}

			// app db
			db, err := openDB(home, server.GetAppDBBackend(ctx.Viper))
			if err != nil {
				return err
			}

			// evolve db
			rawEvolveDB, err := openRawEvolveDB(home)
			if err != nil {
				return err
			}
			defer func() {
				if closeErr := rawEvolveDB.Close(); closeErr != nil {
					fmt.Printf("Warning: failed to close evolve database: %v\n", closeErr)
				}
			}()

			// prefixed evolve db
			evolveDB := kt.Wrap(rawEvolveDB, &kt.PrefixTransform{
				Prefix: ds.NewKey(node.EvPrefix),
			})

			evolveStore := store.New(evolveDB)
			if height == 0 {
				currentHeight, err := evolveStore.Height(goCtx)
				if err != nil {
					return err
				}

				height = currentHeight - 1
			}

			// rollback ev-node main state
			if err := evolveStore.Rollback(goCtx, height); err != nil {
				return fmt.Errorf("failed to rollback ev-node state: %w", err)
			}

			// rollback ev-node goheader state
			headerStore, err := goheaderstore.NewStore[*evtypes.SignedHeader](
				evolveDB,
				goheaderstore.WithStorePrefix("headerSync"),
				goheaderstore.WithMetrics(),
			)
			if err != nil {
				return err
			}

			dataStore, err := goheaderstore.NewStore[*evtypes.Data](
				evolveDB,
				goheaderstore.WithStorePrefix("dataSync"),
				goheaderstore.WithMetrics(),
			)
			if err != nil {
				return err
			}

			if err := headerStore.Start(goCtx); err != nil {
				return err
			}
			defer func() {
				if err := headerStore.Stop(goCtx); err != nil {
					ctx.Logger.Error("failed to stop header store", "error", err)
				}
			}()

			if err := dataStore.Start(goCtx); err != nil {
				return err
			}
			defer func() {
				if err := dataStore.Stop(goCtx); err != nil {
					ctx.Logger.Error("failed to stop data store", "error", err)
				}
			}()

			if err := headerStore.DeleteFromHead(goCtx, height); err != nil {
				return fmt.Errorf("failed to rollback header sync service state: %w", err)
			}

			if err := dataStore.DeleteFromHead(goCtx, height); err != nil {
				return fmt.Errorf("failed to rollback data sync service state: %w", err)
			}
			// rollback the multistore
			app := appCreator(ctx.Logger, db, nil, ctx.Viper)
			if err := app.CommitMultiStore().RollbackToVersion(int64(height)); err != nil {
				return fmt.Errorf("failed to rollback to version: %w", err)
			}

			fmt.Printf("Rolled back state to height %d\n", height)
			return nil
		},
	}

	cmd.Flags().String(flags.FlagHome, defaultNodeHome, "The application home directory")
	cmd.Flags().Uint64Var(&height, flags.FlagHeight, 0, "rollback to a specific height")
	return cmd
}
