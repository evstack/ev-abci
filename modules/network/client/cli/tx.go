package cli

import (
	"fmt"
	"strconv"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/cosmos/cosmos-sdk/client/tx"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/spf13/cobra"

	"github.com/evstack/ev-abci/modules/network/types"
)

// GetTxCmd returns the transaction commands for this module
func GetTxCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                        types.ModuleName,
		Short:                      fmt.Sprintf("%s transactions subcommands", types.ModuleName),
		DisableFlagParsing:         true,
		SuggestionsMinimumDistance: 2,
		RunE:                       client.ValidateCmd,
	}

	cmd.AddCommand(
		CmdAttest(),
		CmdJoinAttesterSet(),
		CmdLeaveAttesterSet(),
	)

	return cmd
}

// CmdAttest returns a CLI command for creating a MsgAttest transaction
func CmdAttest() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "attest [height] [vote-base64]",
		Short: "Submit an attestation for a checkpoint height",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientTxContext(cmd)
			if err != nil {
				return err
			}

			height, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil {
				return fmt.Errorf("invalid height: %w", err)
			}

			vote := []byte(args[1])
			// Authority is the account that signs and pays for the transaction
			authority := clientCtx.GetFromAddress().String()
			// Consensus address is the validator address
			consensusAddress := sdk.ValAddress(clientCtx.GetFromAddress()).String()
			msg := types.NewMsgAttest(
				authority,
				consensusAddress,
				height,
				vote,
			)

			return tx.GenerateOrBroadcastTxCLI(clientCtx, cmd.Flags(), msg)
		},
	}

	flags.AddTxFlagsToCmd(cmd)

	return cmd
}

// CmdJoinAttesterSet returns a CLI command for creating a MsgJoinAttesterSet transaction
func CmdJoinAttesterSet() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "join-attester",
		Short: "Join the attester set as a validator",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientTxContext(cmd)
			if err != nil {
				return err
			}

			// Get the public key from the flag (same pattern as staking module)
			pubkeyStr, err := cmd.Flags().GetString("pubkey")
			if err != nil {
				return err
			}

			if pubkeyStr == "" {
				return fmt.Errorf("must specify the validator's public key using --pubkey flag")
			}

			// Parse the JSON-encoded public key (same as staking module)
			var pubkey cryptotypes.PubKey
			if err := clientCtx.Codec.UnmarshalInterfaceJSON([]byte(pubkeyStr), &pubkey); err != nil {
				return fmt.Errorf("failed to parse public key: %w", err)
			}

			// Authority is the account that signs and pays for the transaction
			authority := clientCtx.GetFromAddress().String()
			// Consensus address is the validator address
			consensusAddress := sdk.ValAddress(clientCtx.GetFromAddress()).String()
			msg, err := types.NewMsgJoinAttesterSet(authority, consensusAddress, pubkey)
			if err != nil {
				return fmt.Errorf("failed to create MsgJoinAttesterSet: %w", err)
			}

			return tx.GenerateOrBroadcastTxCLI(clientCtx, cmd.Flags(), msg)
		},
	}

	cmd.Flags().String("pubkey", "", "The validator's Protobuf JSON encoded public key")
	cmd.MarkFlagRequired("pubkey")
	flags.AddTxFlagsToCmd(cmd)

	return cmd
}

// CmdLeaveAttesterSet returns a CLI command for creating a MsgLeaveAttesterSet transaction
func CmdLeaveAttesterSet() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "leave-attester",
		Short: "Leave the attester set as a validator",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientTxContext(cmd)
			if err != nil {
				return err
			}

			// Authority is the account that signs and pays for the transaction
			authority := clientCtx.GetFromAddress().String()
			// Consensus address is the validator address
			consensusAddress := sdk.ValAddress(clientCtx.GetFromAddress()).String()
			msg := types.NewMsgLeaveAttesterSet(authority, consensusAddress)

			return tx.GenerateOrBroadcastTxCLI(clientCtx, cmd.Flags(), msg)
		},
	}

	flags.AddTxFlagsToCmd(cmd)

	return cmd
}
