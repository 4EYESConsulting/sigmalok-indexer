package main

import (
	"context"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/anthdm/hollywood/actor"
	"go.uber.org/automaxprocs/maxprocs"
	"go.uber.org/zap"

	"4EYESConsulting/sigmalok-indexer/internal/actors"
	"4EYESConsulting/sigmalok-indexer/internal/config"
	"4EYESConsulting/sigmalok-indexer/internal/database"
	"4EYESConsulting/sigmalok-indexer/internal/logging"
	"4EYESConsulting/sigmalok-indexer/internal/types"

	"github.com/mgpai22/ergo-connector-go/node"
)

var cmdlineFlags struct {
	configFile string
	debug      bool
}

func main() {
	flag.StringVar(
		&cmdlineFlags.configFile,
		"config",
		"",
		"path to config file to load",
	)
	flag.BoolVar(
		&cmdlineFlags.debug,
		"debug",
		false,
		"enable debug logging",
	)
	flag.Parse()

	// Load config
	cfg, err := config.Load(cmdlineFlags.configFile)
	if err != nil {
		fmt.Printf("Failed to load config: %s\n", err)
		os.Exit(1)
	}

	if err := validateConfig(cfg); err != nil {
		fmt.Printf("Configuration validation failed: %s\n", err)
		os.Exit(1)
	}

	if err := logging.Setup(cfg); err != nil {
		fmt.Printf("Failed to setup logger: %s\n", err)
		os.Exit(1)
	}
	logger := logging.GetLogger()

	// Set log level based on debug flag
	if cmdlineFlags.debug {
		logger = logger.Desugar().
			WithOptions(zap.IncreaseLevel(zap.DebugLevel)).
			Sugar()
	}

	// Configure max processes with our logger wrapper, toss undo func
	zapPrintf := func(format string, args ...interface{}) {
		logger.Infof(format, args...)
	}
	_, err = maxprocs.Set(maxprocs.Logger(zapPrintf))
	if err != nil {
		logger.Errorf("failed to set max processes: %v", err)
		os.Exit(1)
	}

	nodeConfig := node.Config{
		BaseURL: cfg.API.NodeURL,
		APIKey:  cfg.API.NodeAPIKey,
	}

	logger.Infof("Connecting to node: %s", nodeConfig.BaseURL)

	nodeProvider, err := node.New(nodeConfig)
	if err != nil {
		logger.Errorf("failed to create node provider: %v", err)
		os.Exit(1)
	}

	nodeInfo, err := nodeProvider.GetNodeInfo(context.Background())
	if err != nil {
		logger.Errorf("failed to get node info: %v", err)
		os.Exit(1)
	}
	network := *nodeInfo.Network
	logger.Infof(
		"Connected to node: %s on network %s, at height %d",
		nodeConfig.BaseURL,
		network,
		*nodeInfo.FullHeight,
	)

	if !strings.EqualFold(network, cfg.Network) {
		logger.Errorf(
			"node is on network %s, expected %s",
			network,
			cfg.Network,
		)
		os.Exit(1)
	}

	engine, err := actor.NewEngine(actor.NewEngineConfig())
	if err != nil {
		logger.Errorf("failed to create actor engine: %v", err)
		os.Exit(1)
	}

	db, err := database.New(cfg)
	if err != nil {
		logger.Errorf("failed to create database: %v", err)
		os.Exit(1)
	}

	cursorPoints, err := db.GetCursorPoints()
	if err != nil {
		logger.Errorf("failed to get cursor points: %v", err)
		os.Exit(1)
	}

	var height uint64
	var hash string

	if len(cursorPoints) > 0 {
		// this sets slot and hash to the most recent cursor point
		height = cursorPoints[0].Height
		hash = hex.EncodeToString(cursorPoints[0].Hash)
	} else {
		height = cfg.Indexer.InterceptHeight
	}

	strategyManagerPID := engine.Spawn(
		actors.NewStrategyManagerActor(
			cfg,
			db,
			*nodeProvider,
			uint64(*nodeInfo.FullHeight),
		),
		"strategyManager",
		actor.WithID("manager"),
		actor.WithMaxRestarts(5),
		actor.WithRestartDelay(30*time.Second),
	)
	logger.Infow("StrategyManagerActor spawned", "pid", strategyManagerPID)

	// Load strategy immediately to avoid losing events during startup
	chainEventCfg := types.StrategyConfig{
		ID:               "chain-event-processor",
		Kind:             "chain-event-processor",
		StartBlockHeight: height,
		StartBlockHash:   hash,
	}
	engine.Send(
		strategyManagerPID,
		actors.LoadStrategyCommand{Config: chainEventCfg},
	)
	logger.Debug("chain event strategy loaded")

	// Load API actor through strategy manager
	apiCfg := types.StrategyConfig{
		ID:   "api",
		Kind: "api",
	}
	engine.Send(
		strategyManagerPID,
		actors.LoadStrategyCommand{Config: apiCfg},
	)
	logger.Debug("api strategy loaded")

	// Load health check strategy
	healthCheckCfg := types.StrategyConfig{
		ID:   "health-check",
		Kind: "health-check",
	}
	engine.Send(
		strategyManagerPID,
		actors.LoadStrategyCommand{Config: healthCheckCfg},
	)
	logger.Debug("health check strategy loaded")

	logger.Infof("offchain indexer started successfully on %s", cfg.Network)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigCh

	logger.Infow("received shutdown signal", "signal", sig.String())
	logger.Info("shutting down...")

	// Create a timeout context for shutdown
	shutdownCtx, shutdownCancel := context.WithTimeout(
		context.Background(),
		10*time.Second,
	)
	defer shutdownCancel()

	// Shutdown actors with timeout
	done := make(chan struct{})
	go func() {
		defer close(done)
		strategyManagerDone := engine.Poison(strategyManagerPID)
		<-strategyManagerDone.Done()
	}()

	select {
	case <-done:
		logger.Info("shutdown complete")
	case <-shutdownCtx.Done():
		logger.Warn("shutdown timed out, forcing exit")
	}
}

func validateConfig(cfg *config.Config) error {
	if cfg.Network == "" {
		return fmt.Errorf("network is required")
	}

	if cfg.Storage.Directory == "" {
		return fmt.Errorf("storage.dir cannot be empty")
	}

	k := cfg.Kafka
	if len(k.Brokers) == 0 {
		return fmt.Errorf("kafka.brokers cannot be empty")
	}
	if k.BlockTopic == "" {
		return fmt.Errorf("kafka.blockTopic cannot be empty")
	}
	if k.TxTopic == "" {
		return fmt.Errorf("kafka.txTopic cannot be empty")
	}
	if k.MempoolTopic == "" {
		return fmt.Errorf("kafka.mempoolTopic cannot be empty")
	}
	if k.MaxBytes <= k.MinBytes {
		return fmt.Errorf("kafka.maxBytes must be greater than kafka.minBytes")
	}

	return nil
}
