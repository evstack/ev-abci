package integration_test

import (
	"context"
	"fmt"
	"github.com/celestiaorg/tastora/framework/types"

	"github.com/celestiaorg/tastora/framework/docker/container"
	"go.uber.org/zap"
)

const (
	attesterDefaultImage   = "evabci/gm"
	attesterDefaultVersion = "local"
	attesterDefaultUIDGID  = "1000:1000"
	attesterHomeDir        = "/home/gm"
)

var AttesterNode = AttesterNodeType{}

type AttesterNodeType struct{}

func (attesterNodeType AttesterNodeType) String() string {
	return "attester"
}

type Attester struct {
	*container.Node
}

func NewAttester(ctx context.Context, dockerClient types.TastoraDockerClient, testName, networkID string, index int, logger *zap.Logger) (*Attester, error) {
	image := container.Image{
		Repository: attesterDefaultImage,
		Version:    attesterDefaultVersion,
		UIDGID:     attesterDefaultUIDGID,
	}

	if logger == nil {
		logger = zap.NewNop()
	}

	node := container.NewNode(
		networkID,
		dockerClient,
		testName,
		image,
		attesterHomeDir,
		index,
		AttesterNode,
		logger,
	)

	attester := &Attester{
		Node: node,
	}

	lifecycle := container.NewLifecycle(logger, dockerClient, attester.Name())
	attester.SetContainerLifecycle(lifecycle)

	err := attester.CreateAndSetupVolume(ctx, attester.Name())
	if err != nil {
		return nil, err
	}

	return attester, nil
}

// Name returns the hostname of the docker container.
func (h *Attester) Name() string {
	return fmt.Sprintf("%s-%d-attester", SanitizeContainerName(h.TestName), h.Index)
}

// AttesterConfig holds configuration for the attester
type AttesterConfig struct {
	ChainID            string
	GMNodeURL          string
	PrivKeyArmor       string
	ValidatorKeyPath   string
	ValidatorStatePath string
}

// DefaultAttesterConfig returns a default configuration
func DefaultAttesterConfig() AttesterConfig {
	return AttesterConfig{
		ChainID: "gm",
	}
}

// createClientConfig creates a basic client.toml before running any gmd commands.
func (h *Attester) createClientConfig(ctx context.Context, chainID, nodeURL string) error {
	// creating a config is required before calling init because tx flags are registered in the attester command.
	clientToml := fmt.Sprintf(`chain-id = "%s"
keyring-backend = "test"
output = "text"
node = "%s"
broadcast-mode = "sync"
`, chainID, nodeURL)

	err := h.WriteFile(ctx, "config/client.toml", []byte(clientToml))
	if err != nil {
		return fmt.Errorf("failed to write client.toml: %w", err)
	}

	return nil
}

// Init initializes the attester home directory with necessary config files
func (h *Attester) Init(ctx context.Context, chainID, nodeURL string) error {
	err := h.createClientConfig(ctx, chainID, nodeURL)
	if err != nil {
		return err
	}

	cmd := []string{
		"gmd",
		"init",
		"attester-node",
		"--chain-id", chainID,
		"--home", attesterHomeDir,
	}

	_, _, err = h.Exec(ctx, h.Logger, cmd, nil)
	if err != nil {
		return fmt.Errorf("failed to initialize attester home: %w", err)
	}

	return nil
}

func (h *Attester) Start(ctx context.Context, config AttesterConfig) error {
	cmd := []string{
		"gmd",
		"attester",
		"--chain-id", config.ChainID,
		"--node", config.GMNodeURL,
		"--home", attesterHomeDir,
		"--priv-key-armor", config.PrivKeyArmor,
		"--verbose",
	}

	err := h.CreateContainer(ctx, h.TestName, h.NetworkID, h.Image, nil, "", h.Bind(), nil, h.Name(), cmd, nil, []string{})
	if err != nil {
		return fmt.Errorf("failed to create attester container: %w", err)
	}

	if err := h.StartContainer(ctx); err != nil {
		return fmt.Errorf("failed to start attester container: %w", err)
	}

	return nil
}
