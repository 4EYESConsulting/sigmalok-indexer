package actors

import (
	"4EYESConsulting/sigmalok-indexer/internal/config"
	"4EYESConsulting/sigmalok-indexer/internal/database"
	"4EYESConsulting/sigmalok-indexer/internal/strategy"
	"4EYESConsulting/sigmalok-indexer/internal/types"

	"github.com/anthdm/hollywood/actor"
	"github.com/mgpai22/ergo-connector-go/node"
)

// StrategyRegistry holds factory functions for creating strategy actors
type StrategyRegistry struct {
	factories map[string]func(types.StrategyConfig, *config.Config, *database.Database, node.NodeProvider) actor.Producer
}

// NewStrategyRegistry creates a new strategy registry with all available strategies
func NewStrategyRegistry() *StrategyRegistry {
	registry := &StrategyRegistry{
		factories: make(
			map[string]func(types.StrategyConfig, *config.Config, *database.Database, node.NodeProvider) actor.Producer,
		),
	}

	registry.factories["chain-event-processor"] = func(cfg types.StrategyConfig, appCfg *config.Config, db *database.Database, nodeProvider node.NodeProvider) actor.Producer {
		return strategy.NewChainEventProcessorStrategy(
			cfg,
			appCfg,
			db,
			nodeProvider,
		)
	}

	registry.factories["api"] = func(cfg types.StrategyConfig, appCfg *config.Config, db *database.Database, nodeProvider node.NodeProvider) actor.Producer {
		return NewAPIActor(
			appCfg,
			db,
			nil,
		) // indexerPID will be set by the strategy manager
	}

	return registry
}

// CreateStrategy creates a strategy actor producer for the given configuration
func (r *StrategyRegistry) CreateStrategy(
	cfg types.StrategyConfig,
	appCfg *config.Config,
	db *database.Database,
	nodeProvider node.NodeProvider,
) (actor.Producer, bool) {
	factory, exists := r.factories[cfg.Kind]
	if !exists {
		return nil, false
	}
	return factory(cfg, appCfg, db, nodeProvider), true
}

func (r *StrategyRegistry) GetAvailableStrategies() []string {
	strategies := make([]string, 0, len(r.factories))
	for kind := range r.factories {
		strategies = append(strategies, kind)
	}
	return strategies
}
