package strategy

import (
	"4EYESConsulting/sigmalok-indexer/internal/config"
	"4EYESConsulting/sigmalok-indexer/internal/database"
	"4EYESConsulting/sigmalok-indexer/internal/logging"
	"4EYESConsulting/sigmalok-indexer/internal/metrics"
	"4EYESConsulting/sigmalok-indexer/internal/types"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/anthdm/hollywood/actor"
	"github.com/disgoorg/disgo/webhook"
	"github.com/mgpai22/ergo-connector-go/node"
	"github.com/schollz/progressbar/v3"
	"go.uber.org/zap"

	ergo "github.com/sigmaspace-io/ergo-lib-go"
)

type BlockInfo struct {
	TransactionCount uint64
	Hash             string
	Timestamp        time.Time
	BlockOffset      int64
}

type ChainEventProcessorActor struct {
	config    types.StrategyConfig
	appCfg    *config.Config
	logger    *zap.SugaredLogger
	engine    *actor.Engine
	selfPID   *actor.PID
	parentPID *actor.PID // Reference to StrategyManagerActor

	db           *database.Database
	nodeProvider node.NodeProvider

	//nolint:unused
	discordClient webhook.Client

	startBlockHeight uint64

	processingFailed bool
	failedAtHeight   uint64

	progressBar *progressbar.ProgressBar

	lastSuccessfullyIndexedBlockHeight uint64
	lastSuccessfullyIndexedBlockHash   string

	blockInfoMap map[uint64]*BlockInfo

	// Mempool transaction tracking
	mempoolTxMap     map[string]*MempoolTxInfo // Maps tx hash to mempool info
	currentTipHeight uint64                    // Track current tip height for mempool processing
}

// LokBoxWithRegisters holds a lok box along with its parsed register values
type LokBoxWithRegisters struct {
	Box       ergo.Box
	Registers *LokBoxRegisters
}

func NewChainEventProcessorStrategy(
	cfg types.StrategyConfig,
	appCfg *config.Config,
	db *database.Database,
	nodeProvider node.NodeProvider,
) actor.Producer {
	return func() actor.Receiver {
		return &ChainEventProcessorActor{
			config: cfg,
			appCfg: appCfg,
			logger: logging.GetLogger().
				With("actor", "ChainEventProcessor", "strategy_id", cfg.ID),
			processingFailed:                   false,
			startBlockHeight:                   cfg.StartBlockHeight,
			failedAtHeight:                     0,
			lastSuccessfullyIndexedBlockHeight: cfg.StartBlockHeight,
			lastSuccessfullyIndexedBlockHash:   cfg.StartBlockHash,
			blockInfoMap:                       make(map[uint64]*BlockInfo),
			mempoolTxMap:                       make(map[string]*MempoolTxInfo),
			currentTipHeight:                   cfg.StartBlockHeight,
			db:                                 db,
			nodeProvider:                       nodeProvider,
		}
	}
}

func (s *ChainEventProcessorActor) Receive(c *actor.Context) {

	switch msg := c.Message().(type) {
	case actor.Initialized:
		metrics.RecordActorMessage("ChainEventProcessor", "Initialized")
		s.logger.Debug("initializing ChainEvent strategy")
		s.engine = c.Engine()
		s.selfPID = c.PID()

	case actor.Started:
		metrics.RecordActorMessage("ChainEventProcessor", "Started")
		s.logger.Debug("ChainEvent strategy started")

	case actor.Stopped:
		metrics.RecordActorMessage("ChainEventProcessor", "Stopped")
		s.logger.Debug("ChainEvent strategy stopped")

	case types.IndexerBlockEvent:
		metrics.RecordActorMessage("ChainEventProcessor", "BlockEvent")
		s.logger.Debugw("received IndexerBlockEvent",
			"block_number", msg.BlockNumber,
			"block_hash", msg.BlockHash,
			"tip_reached", msg.TipReached)

		s.blockInfoMap[msg.BlockNumber] = &BlockInfo{
			TransactionCount: uint64(msg.NumTxs),
			Hash:             msg.BlockHash,
			Timestamp:        time.Unix(msg.Timestamp, 0),
			BlockOffset:      msg.BlockOffset,
		}

		if err := s.processBlockEvent(msg); err != nil {
			metrics.RecordProcessingError("ChainEventProcessor", "BlockProcessing")
			s.logger.Errorf("error processing block event: %v", err)
		} else {
			metrics.MetricEventsProcessed.Inc()
			s.lastSuccessfullyIndexedBlockHeight = msg.BlockNumber
			s.lastSuccessfullyIndexedBlockHash = msg.BlockHash
		}

	case types.IndexerTransactionEvent:
		metrics.RecordActorMessage("ChainEventProcessor", "TransactionEvent")
		s.logger.Debugw("received IndexerTransactionEvent",
			"transaction_id", msg.Transaction,
			"height", msg.Height,
			"strategy_id", s.config.ID)

		if s.processingFailed {
			s.logger.Warnw("dropping IndexerTransactionEvent due to previous processing failure",
				"height", msg.Height,
				"failed_at_height", s.failedAtHeight)
			return
		}

		func() {
			defer func() {
				if r := recover(); r != nil {
					metrics.RecordProcessingError("ChainEventProcessor", "TransactionPanic")
					s.setProcessingFailed(msg.Height, fmt.Sprintf("panic in processTransactionEvent: %v", r))
				}
			}()

			if err := s.processTransactionEvent(msg); err != nil {
				metrics.RecordProcessingError("ChainEventProcessor", "TransactionProcessing")
				s.setProcessingFailed(msg.Height, fmt.Sprintf("error in processTransactionEvent: %v", err))
			} else {
				metrics.MetricEventsProcessed.Inc()
				blockInfo, exists := s.blockInfoMap[msg.Height]
				if exists {
					// decr tx count to track when all txns are processed
					blockInfo.TransactionCount = blockInfo.TransactionCount - 1
				} else {
					s.logger.Errorf("block info not found for block %d", msg.Height)
				}
			}
		}()

		if blockInfo, exists := s.blockInfoMap[msg.Height]; exists {

			if blockInfo.TransactionCount == 0 {
				s.logger.Debug("All transactions for block processed")

				blockHash, err := hex.DecodeString(blockInfo.Hash)
				if err != nil {
					s.logger.Errorf("failed to decode block hash: %v", err)
					return
				}

				txHash := []byte{}
				if msg.ParsedTx != nil {
					txHash, err = hex.DecodeString(msg.ParsedTx.ID)
					if err != nil {
						s.logger.Errorf("failed to decode transaction hash: %v", err)
					}
				}

				if err := s.db.AddCursorPoint(database.CursorPoint{
					Height:      msg.Height,
					Hash:        blockHash,
					BlockOffset: blockInfo.BlockOffset,
					TxOffset:    msg.TxOffset,
					TxHash:      txHash,
				}); err != nil {
					s.logger.Errorf("failed to update cursor: %v", err)
				}
			}
		}

	case types.IndexerTransactionUnappliedEvent:
		metrics.RecordActorMessage("ChainEventProcessor", "TransactionUnappliedEvent")
		s.logger.Debugw("received IndexerTransactionUnappliedEvent",
			"transaction_id", msg.Transaction,
			"strategy_id", s.config.ID)

		if s.processingFailed {
			s.logger.Warnw("dropping IndexerTransactionUnappliedEvent due to previous processing failure",
				"failed_at_height", s.failedAtHeight)
			return
		}

		func() {
			defer func() {
				if r := recover(); r != nil {
					metrics.RecordProcessingError("ChainEventProcessor", "TransactionUnappliedPanic")
					s.logger.Errorf("panic in processTransactionUnappliedEvent: %v", r)
				}
			}()

			if err := s.processTransactionUnappliedEvent(msg); err != nil {
				metrics.RecordProcessingError("ChainEventProcessor", "TransactionUnappliedProcessing")
				s.logger.Errorf("error in processTransactionUnappliedEvent: %v", err)
			} else {
				metrics.MetricEventsProcessed.Inc()
			}
		}()

	case types.IndexerRollbackEvent:
		metrics.RecordActorMessage("ChainEventProcessor", "RollbackEvent")
		s.logger.Warnw("received IndexerRollbackEvent",
			"height", msg.Height,
			"block_hash", msg.BlockHash)

		if s.processingFailed {
			s.logger.Warnw("dropping IndexerRollbackEvent due to previous processing failure",
				"height", msg.Height,
				"failed_at_height", s.failedAtHeight)
			return
		}

		func() {
			defer func() {
				if r := recover(); r != nil {
					metrics.RecordProcessingError("ChainEventProcessor", "RollbackPanic")
					s.setProcessingFailed(msg.Height, fmt.Sprintf("panic in processRollbackEvent: %v", r))
				}
			}()

			if err := s.processRollbackEvent(msg); err != nil {
				metrics.RecordProcessingError("ChainEventProcessor", "RollbackProcessing")
				s.setProcessingFailed(msg.Height, fmt.Sprintf("error in processRollbackEvent: %v", err))
			}
		}()

	case types.IndexerMempoolAcceptedEvent:
		metrics.RecordActorMessage("ChainEventProcessor", "MempoolAcceptedEvent")
		s.logger.Debugw("received IndexerMempoolAcceptedEvent",
			"transaction_id", msg.ParsedTx.ID,
			"strategy_id", s.config.ID)

		func() {
			defer func() {
				if r := recover(); r != nil {
					metrics.RecordProcessingError("ChainEventProcessor", "MempoolAcceptedPanic")
					s.logger.Errorf("panic in processMempoolAcceptedEvent: %v", r)
				}
			}()

			if err := s.processMempoolAcceptedEvent(msg); err != nil {
				metrics.RecordProcessingError("ChainEventProcessor", "MempoolAcceptedProcessing")
				s.logger.Errorf("error in processMempoolAcceptedEvent: %v", err)
			} else {
				metrics.MetricEventsProcessed.Inc()
			}
		}()

	case types.IndexerMempoolWithdrawnEvent:
		metrics.RecordActorMessage("ChainEventProcessor", "MempoolWithdrawnEvent")
		s.logger.Debugw("received IndexerMempoolWithdrawnEvent",
			"transaction_id", msg.ParsedTx.ID,
			"strategy_id", s.config.ID)

	// func() {
	// 	defer func() {
	// 		if r := recover(); r != nil {
	// 			metrics.RecordProcessingError("ChainEventProcessor", "MempoolWithdrawnPanic")
	// 			s.logger.Errorf("panic in processMempoolWithdrawnEvent: %v", r)
	// 		}
	// 	}()

	// 	if err := s.processMempoolWithdrawnEvent(msg); err != nil {
	// 		metrics.RecordProcessingError("ChainEventProcessor", "MempoolWithdrawnProcessing")
	// 		s.logger.Errorf("error in processMempoolWithdrawnEvent: %v", err)
	// 	} else {
	// 		metrics.MetricEventsProcessed.Inc()
	// 	}
	// }()

	case types.IndexerStatusEvent:
		metrics.RecordActorMessage("ChainEventProcessor", "StatusEvent")
		s.logger.Debugw("received IndexerStatusEvent",
			"cursor_height", msg.CursorHeight,
			"cursor_hash", msg.CursorHash,
			"tip_height", msg.TipHeight,
			"tip_hash", msg.TipHash,
			"tip_reached", msg.TipReached)

		// Update current tip height for mempool processing
		s.currentTipHeight = msg.TipHeight

		// Perform garbage collection of confirmed mempool transactions
		s.garbageCollectMempoolTxs(msg.CursorHeight)

		if !msg.TipReached && msg.TipHeight > s.startBlockHeight {
			max := int64(msg.TipHeight - s.startBlockHeight)
			current := int64(msg.CursorHeight - s.startBlockHeight)
			if s.progressBar == nil {
				if s.startBlockHeight < msg.CursorHeight {
					s.startBlockHeight = msg.CursorHeight
					max = int64(msg.TipHeight - s.startBlockHeight)
					current = int64(msg.CursorHeight - s.startBlockHeight)
				}
				s.logger.Infof("starting progress bar with max %d, tip height %d, cursor height %d, start block height %d", max, msg.TipHeight, msg.CursorHeight, s.startBlockHeight)
				s.progressBar = progressbar.Default(
					max,
					"Indexer sync progress",
				)
			} else {
				s.progressBar.ChangeMax64(max)
			}
			if err := s.progressBar.Set64(current); err != nil {
				s.logger.Warnf("Failed to set progress bar value: %v", err)
			}
		} else if msg.TipReached && s.progressBar != nil {
			if err := s.progressBar.Finish(); err != nil {
				s.logger.Warnf("Failed to finish progress bar: %v", err)
			}
			_ = s.progressBar.Close()
			s.progressBar = nil
		}

	case types.SetParentPID:
		metrics.RecordActorMessage("ChainEventProcessor", "SetParentPID")
		s.logger.Debugw("parent PID set", "pid", msg.PID)
		s.parentPID = msg.PID

	default:
		metrics.RecordActorMessage("ChainEventProcessor", "UnknownMessage")
	}
}

func (s *ChainEventProcessorActor) processBlockEvent(
	blockEvent types.IndexerBlockEvent,
) error {
	s.logger.Debugw("processing block event",
		"block_number", blockEvent.BlockNumber,
		"block_hash", blockEvent.BlockHash,
		"num_txs", blockEvent.NumTxs,
		"tip_reached", blockEvent.TipReached,
		"strategy_id", s.config.ID)

	if blockEvent.TipReached {
		return s.processBlockTipReached(blockEvent)
	}

	return nil
}

func (s *ChainEventProcessorActor) setProcessingFailed(
	height uint64,
	reason string,
) {
	s.processingFailed = true
	s.failedAtHeight = height

	s.logger.Errorw(
		"setting processing failed state - all subsequent events will be blocked",
		"failed_at_height",
		s.failedAtHeight,
		"reason",
		reason,
	)

}

func (s *ChainEventProcessorActor) processBlockTipReached(
	blockEvent types.IndexerBlockEvent,
) error {
	s.logger.Debug("Processing block tip reached...")

	// trigger a restart based on some check
	// if false {
	// 	s.triggerIndexerRestart("", s.lastSucessfullyIndexedSlot, s.lastSucessfullyIndexedBlockHash)
	// }

	return nil
}

func (s *ChainEventProcessorActor) processTransactionEvent(
	txEvent types.IndexerTransactionEvent,
) error {
	s.logger.Debugw("processing transaction event",
		"height", txEvent.Height,
		"strategy_id", s.config.ID)

	tx := txEvent.ParsedTx
	if tx == nil {
		return fmt.Errorf("no parsed transaction available in event")
	}

	blockInfo, exists := s.blockInfoMap[txEvent.Height]
	if !exists {
		return fmt.Errorf("block info not found for height %d", txEvent.Height)
	}
	blockHash := blockInfo.Hash
	blockTimestamp := blockInfo.Timestamp

	isSigmaLokTransaction := false
	sigmaLokInputIndex := -1
	sigmaLokOutputIndex := -1

	for i, input := range tx.Inputs {
		if input.ErgoTree == s.appCfg.Contract.Ergotree {
			isSigmaLokTransaction = true
			sigmaLokInputIndex = i
			break
		}
	}

	// iteration is needed since genesis output could technically be anywhere
	for i, output := range tx.Outputs {
		if output.ErgoTree == s.appCfg.Contract.Ergotree {
			isSigmaLokTransaction = true
			sigmaLokOutputIndex = i
			s.logger.Infof("ergotree found in transaction %s", tx.ID)
			break
		}
	}

	if !isSigmaLokTransaction {
		return nil
	}

	ctx := TxContext{
		BlockHeight: txEvent.Height,
		BlockHash:   blockHash,
		Timestamp:   blockTimestamp,
		Confirmed:   true,
		TipHeight:   s.currentTipHeight,
	}

	return s.processLokTransaction(
		ctx,
		tx,
		sigmaLokInputIndex,
		sigmaLokOutputIndex,
	)

}

func (s *ChainEventProcessorActor) processRollbackEvent(
	rollbackEvent types.IndexerRollbackEvent,
) error {
	s.logger.Warnw("processing rollback event",
		"height", rollbackEvent.Height,
		"block_hash", rollbackEvent.BlockHash,
		"strategy_id", s.config.ID)

	// Clear all block info entries for blocks >= rollback height
	for blockHeight := range s.blockInfoMap {
		if blockHeight >= rollbackEvent.Height {
			delete(s.blockInfoMap, blockHeight)
		}
	}

	// Rollback cursor entries in database for blocks >= rollback height
	if err := s.db.RollbackCursor(rollbackEvent.Height); err != nil {
		s.logger.Errorw("failed to rollback cursor entries",
			"height", rollbackEvent.Height,
			"error", err)
		return fmt.Errorf("failed to rollback cursor entries: %w", err)
	}

	// Update the last successfully indexed height if needed
	if s.lastSuccessfullyIndexedBlockHeight >= rollbackEvent.Height {
		// Find the highest block we still have that's below the rollback height
		highestValidHeight := uint64(0)
		highestValidHash := ""

		for blockHeight, blockInfo := range s.blockInfoMap {
			if blockHeight > highestValidHeight {
				highestValidHeight = blockHeight
				highestValidHash = blockInfo.Hash
			}
		}

		// If we have valid blocks, use the highest one, otherwise use the start block
		if highestValidHeight > 0 {
			s.lastSuccessfullyIndexedBlockHeight = highestValidHeight
			s.lastSuccessfullyIndexedBlockHash = highestValidHash
		} else {
			s.lastSuccessfullyIndexedBlockHeight = s.config.StartBlockHeight
			s.lastSuccessfullyIndexedBlockHash = s.config.StartBlockHash
		}

		s.logger.Infow("updated last successfully indexed block after rollback",
			"height", s.lastSuccessfullyIndexedBlockHeight,
			"hash", s.lastSuccessfullyIndexedBlockHash)
	}

	err := s.db.Rollback(rollbackEvent.Height)
	if err != nil {
		return fmt.Errorf("failed to rollback database: %w", err)
	}

	s.logger.Infow("rollback processing completed",
		"rollback_height", rollbackEvent.Height,
		"cleared_blocks_count", len(s.blockInfoMap),
		"last_valid_height", s.lastSuccessfullyIndexedBlockHeight)

	return nil
}

func (s *ChainEventProcessorActor) processTransactionUnappliedEvent(
	txEvent types.IndexerTransactionUnappliedEvent,
) error {
	s.logger.Infow("processing transaction unapplied event",
		"transaction_id", txEvent.Transaction,
		"strategy_id", s.config.ID)

	return nil
}

func (s *ChainEventProcessorActor) processMempoolAcceptedEvent(
	mempoolEvent types.IndexerMempoolAcceptedEvent,
) error {
	s.logger.Debugw("processing mempool accepted event",
		"strategy_id", s.config.ID)

	// Use the pre-decoded transaction from the event
	tx := mempoolEvent.ParsedTx
	if tx == nil {
		s.logger.Errorw(
			"no parsed transaction available in mempool accepted event",
			"transaction",
			mempoolEvent.Transaction,
		)
		return fmt.Errorf(
			"no parsed transaction available in mempool accepted event",
		)
	}

	// Check if this is a SigmaLok transaction
	isSigmaLokTransaction := false
	sigmaLokOutputIndex := -1
	sigmaLokInputIndex := -1

	for i, input := range tx.Inputs {
		if input.ErgoTree == s.appCfg.Contract.Ergotree {
			isSigmaLokTransaction = true
			sigmaLokInputIndex = i
			break
		}
	}

	// Look for SigmaLok output (genesis transactions have outputs)
	for i, output := range tx.Outputs {
		if output.ErgoTree == s.appCfg.Contract.Ergotree {
			isSigmaLokTransaction = true
			sigmaLokOutputIndex = i
			s.logger.Infof(
				"SigmaLok ergotree found in mempool transaction %s",
				tx.ID,
			)
			break
		}
	}

	if !isSigmaLokTransaction {
		return nil
	}

	ctx := TxContext{
		BlockHeight: 0,
		BlockHash:   "",
		Timestamp:   time.Now(),
		Confirmed:   false,
		TipHeight:   s.currentTipHeight,
	}

	return s.processLokTransaction(
		ctx,
		tx,
		sigmaLokInputIndex,
		sigmaLokOutputIndex,
	)
}

//nolint:unused
func (s *ChainEventProcessorActor) triggerIndexerRestart(
	reason string,
	restartHeight uint64,
) {
	s.logger.Warnw(
		"triggering indexer restart",
		"reason",
		reason,
		"restart_height",
		restartHeight,
	)

	restartRequest := types.IndexerRestartRequest{
		Reason:        reason,
		StrategyPID:   s.selfPID, // Include our PID so the manager can restart self
		RestartHeight: restartHeight,
	}

	if s.engine != nil && s.parentPID != nil {
		s.engine.Send(s.parentPID, restartRequest)
		s.logger.Infow(
			"indexer restart request sent to strategy manager",
			"reason",
			reason,
			"strategy_pid",
			s.selfPID.String(),
			"restart_height",
			restartHeight,
		)
	} else {
		s.logger.Error("cannot trigger indexer restart: engine or parent PID not available")
	}
}
