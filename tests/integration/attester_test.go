package integration_test

import (
	"context"
	"fmt"

	"github.com/celestiaorg/tastora/framework/docker/container"
	dockerclient "github.com/moby/moby/client"
	"go.uber.org/zap"
)

const (
	attesterDefaultImage   = "evabci/gm"
	attesterDefaultVersion = "local"
	attesterDefaultUIDGID  = "2000:2000"
	attesterHomeDir        = "/home/attester"
)

var AttesterNode = AttesterNodeType{}

type AttesterNodeType struct{}

func (attesterNodeType AttesterNodeType) String() string {
	return "attester"
}

type Attester struct {
	*container.Node
}

func NewAttester(ctx context.Context, dockerClient *dockerclient.Client, testName, networkID string, index int, logger *zap.Logger) (*Attester, error) {
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

// Attester inherits WriteFileWithOptions from embedded container.Node.
