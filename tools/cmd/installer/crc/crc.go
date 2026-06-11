package crc

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"github.com/kiali/kiali/tools/cmd/installer/command"
)

const (
	crcBinary   = "crc"
	ocBinary    = "oc"
	apiServer   = "https://api.crc.testing:6443"
	defaultUser = "kubeadmin"
)

type Config struct {
	CPUs                    int
	DiskSize                int
	EnableClusterMonitoring bool
	KubeAdminPassword       string
	Memory                  int
	PullSecretFile          string
}

func NewConfig() *Config {
	return &Config{
		CPUs:                    12,
		DiskSize:                100,
		EnableClusterMonitoring: true,
		KubeAdminPassword:       "kiali",
		Memory:                  32,
		PullSecretFile:          "",
	}
}

func (c *Config) Validate() error {
	if _, err := exec.LookPath(crcBinary); err != nil {
		return fmt.Errorf("%s not found in PATH: %w", crcBinary, err)
	}

	if c.PullSecretFile != "" {
		if _, err := os.Stat(c.PullSecretFile); err != nil {
			return fmt.Errorf("pull secret file not found: %s", c.PullSecretFile)
		}
	}

	return nil
}

func Start(config *Config, logger *zerolog.Logger) error {
	if err := config.Validate(); err != nil {
		return err
	}

	if isRunning(logger) {
		logger.Info().Msg("CRC is already running, skipping start")
		return login(config, logger)
	}

	if err := configure(config, logger); err != nil {
		return fmt.Errorf("configuring CRC: %w", err)
	}

	if err := setup(logger); err != nil {
		return fmt.Errorf("running crc setup: %w", err)
	}

	if err := start(logger); err != nil {
		return fmt.Errorf("running crc start: %w", err)
	}

	if err := waitForAPIServer(logger); err != nil {
		return fmt.Errorf("waiting for API server: %w", err)
	}

	if err := login(config, logger); err != nil {
		return fmt.Errorf("logging into cluster: %w", err)
	}

	if err := exposeImageRegistry(logger); err != nil {
		return fmt.Errorf("exposing image registry: %w", err)
	}

	logger.Info().Msg("CRC cluster is ready")
	logger.Info().Msgf("API Server: %s", apiServer)
	logger.Info().Msgf("Console: https://console-openshift-console.apps-crc.testing")
	logger.Info().Msgf("Username: %s", defaultUser)
	logger.Info().Msgf("Password: %s", config.KubeAdminPassword)

	return nil
}

func isRunning(logger *zerolog.Logger) bool {
	output, err := command.Command(crcBinary, "status").Output()
	if err != nil {
		logger.Info().Msg("CRC does not appear to be running")
		return false
	}
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, "OpenShift:") {
			return strings.Contains(line, "Running")
		}
	}
	return false
}

func configure(config *Config, logger *zerolog.Logger) error {
	if config.PullSecretFile == "" {
		return fmt.Errorf("pull secret file is required (download from https://console.redhat.com/openshift/create/local)")
	}

	logger.Info().Msg("Configuring CRC...")

	memoryMB := strconv.Itoa(config.Memory * 1024)

	settings := []struct {
		key   string
		value string
	}{
		{"consent-telemetry", "no"},
		{"cpus", strconv.Itoa(config.CPUs)},
		{"disable-update-check", "true"},
		{"disk-size", strconv.Itoa(config.DiskSize)},
		{"enable-cluster-monitoring", strconv.FormatBool(config.EnableClusterMonitoring)},
		{"kubeadmin-password", config.KubeAdminPassword},
		{"memory", memoryMB},
		{"pull-secret-file", config.PullSecretFile},
	}

	for _, s := range settings {
		logger.Info().Msgf("  %s = %s", s.key, s.value)
		if err := command.Command(crcBinary, "config", "set", s.key, s.value).Run(); err != nil {
			return fmt.Errorf("setting %s: %w", s.key, err)
		}
	}

	return nil
}

func setup(logger *zerolog.Logger) error {
	logger.Info().Msg("Running crc setup (this may take a few minutes)...")
	return command.Command(crcBinary, "setup").Run()
}

func start(logger *zerolog.Logger) error {
	logger.Info().Msg("Starting CRC (this may take several minutes)...")
	return command.Command(crcBinary, "start").Run()
}

func waitForAPIServer(logger *zerolog.Logger) error {
	logger.Info().Msg("Waiting for OpenShift API server to become ready...")
	deadline := time.Now().Add(10 * time.Minute)
	for {
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting for API server to become ready")
		}

		if isRunning(logger) {
			return nil
		}

		time.Sleep(15 * time.Second)
	}
}

func login(config *Config, logger *zerolog.Logger) error {
	logger.Info().Msgf("Logging into cluster as %s...", defaultUser)
	return command.Command(ocBinary, "login",
		"-u", defaultUser,
		"-p", config.KubeAdminPassword,
		"--server", apiServer,
		"--insecure-skip-tls-verify=true",
	).Run()
}

func exposeImageRegistry(logger *zerolog.Logger) error {
	logger.Info().Msg("Exposing internal image registry via default route...")
	return command.Command(ocBinary, "patch",
		"config.imageregistry.operator.openshift.io/cluster",
		"--patch", `{"spec":{"defaultRoute":true}}`,
		"--type=merge",
	).Run()
}
