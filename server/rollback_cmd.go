package server

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/cosmos/cosmos-sdk/server"
	"github.com/cosmos/cosmos-sdk/server/types"
	"github.com/evstack/ev-node/pkg/store"
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
			db, err := openDB(home, server.GetAppDBBackend(ctx.Viper))
			if err != nil {
				return err
			}
			app := appCreator(ctx.Logger, db, nil, ctx.Viper)

			// rollback ev-node state
			evolveDB, err := openEvolveDB(home)
			if err != nil {
				return err
			}
			defer evolveDB.Close()

			evolveStore := store.New(evolveDB)
			if height == 0 {
				currentHeight, err := evolveStore.Height(cmd.Context())
				if err != nil {
					return err
				}

				height = currentHeight - 1
			}

			if err := evolveStore.Rollback(cmd.Context(), height); err != nil {
				return fmt.Errorf("failed to rollback ev-node state: %w", err)
			}

			// rollback the multistore
			if err := app.CommitMultiStore().RollbackToVersion(int64(height)); err != nil {
				return fmt.Errorf("failed to rollback to version: %w", err)
			}

			fmt.Printf("Rolled back state to height %d", height)
			return nil
		},
	}

	cmd.Flags().String(flags.FlagHome, defaultNodeHome, "The application home directory")
	cmd.Flags().Uint64Var(&height, flags.FlagHeight, 0, "rollback to a specific height")
	return cmd
}
