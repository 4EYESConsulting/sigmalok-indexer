package actors

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/anthdm/hollywood/actor"
	"go.uber.org/zap"

	"4EYESConsulting/sigmalok-indexer/internal/api"
	"4EYESConsulting/sigmalok-indexer/internal/config"
	"4EYESConsulting/sigmalok-indexer/internal/database"
	"4EYESConsulting/sigmalok-indexer/internal/logging"
	"4EYESConsulting/sigmalok-indexer/internal/types"
)

type APIActor struct {
	cfg        *config.Config
	db         *database.Database
	logger     *zap.SugaredLogger
	engine     *actor.Engine
	selfPID    *actor.PID
	indexerPID *actor.PID
	server     *http.Server
}

func NewAPIActor(
	cfg *config.Config,
	db *database.Database,
	indexerPID *actor.PID,
) actor.Producer {
	return func() actor.Receiver {
		return &APIActor{
			cfg:        cfg,
			db:         db,
			logger:     logging.GetLogger().With("actor", "APIActor"),
			indexerPID: indexerPID,
		}
	}
}

func (a *APIActor) Receive(c *actor.Context) {
	switch msg := c.Message().(type) {
	case actor.Initialized:
		a.engine, a.selfPID = c.Engine(), c.PID()

	case actor.Started:
		// Wait for indexerPID before starting API server
		if a.indexerPID != nil {
			if err := a.startAPI(); err != nil {
				a.logger.Fatalw("failed to start API server", "err", err)
			}
		} else {
			a.logger.Info("waiting for indexer PID before starting API server")
		}

	case actor.Stopped:
		a.shutdown()

	case types.SetParentPID:

	case SetIndexerPID:
		a.logger.Info("received indexer PID", "pid", msg.PID)
		a.indexerPID = msg.PID
		if err := a.startAPI(); err != nil {
			a.logger.Fatalw("failed to start API server", "err", err)
		}
	}
}

// SetIndexerPID is a message to set the indexer PID
type SetIndexerPID struct {
	PID *actor.PID
}

func (a *APIActor) startAPI() error {
	if a.indexerPID == nil {
		return fmt.Errorf("cannot start API server: indexer PID not set")
	}

	server, err := api.Start(a.cfg, a.db, a.indexerPID, a.engine)
	if err != nil {
		return fmt.Errorf("failed to start API server: %w", err)
	}
	a.server = server
	return nil
}

func (a *APIActor) shutdown() {
	if a.server != nil {
		a.logger.Info("shutting down API server")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err := a.server.Shutdown(ctx); err != nil {
			a.logger.Warnw("API server shutdown error", "err", err)
		} else {
			a.logger.Info("API server shutdown complete")
		}
	}
}
