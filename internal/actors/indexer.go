package actors

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/anthdm/hollywood/actor"
	"github.com/mgpai22/ergo-connector-go/node"
	"github.com/segmentio/kafka-go"
	"go.uber.org/zap"

	"4EYESConsulting/sigmalok-indexer/internal/config"
	"4EYESConsulting/sigmalok-indexer/internal/database"
	"4EYESConsulting/sigmalok-indexer/internal/logging"
	"4EYESConsulting/sigmalok-indexer/internal/metrics"
	"4EYESConsulting/sigmalok-indexer/internal/types"
)

const (
	// ErrorReportThreshold is the number of consecutive errors before reporting reader issues
	ErrorReportThreshold = 5
	// MaxOffsetEntries is the maximum number of processed offsets to keep in memory
	MaxOffsetEntries = 100000
	// DefaultBackoffInitial is the initial backoff duration for reader errors
	DefaultBackoffInitial = 100 * time.Millisecond
	// MaxBackoff is the maximum backoff duration for reader errors
	MaxBackoff = 5 * time.Second
	// DefaultKafkaTimeout is the timeout for Kafka operations
	DefaultKafkaTimeout = 10 * time.Second
	// CriticalErrorThreshold is the number of consecutive errors before attempting restart
	CriticalErrorThreshold = 20
	// WarningErrorThreshold is the number of consecutive errors before warning
	WarningErrorThreshold = 10
	// DefaultMaxStragglersLookback is the default number of blocks to look back for stragglers
	DefaultMaxStragglersLookback = 100
)

type blockKey struct {
	Height uint64
	Hash   string
}

func (k blockKey) String() string {
	if k.Hash == "" {
		return fmt.Sprintf("height=%d", k.Height)
	}
	return fmt.Sprintf("height=%d,hash=%s", k.Height, k.Hash)
}

type blockState struct {
	expected int64
	seen     int64 // incr every time we process a TX for this block
	rollback bool
}

type txMessageWrapper struct {
	kafkaMsg kafka.Message
	parsedTx *types.Transaction
	parseErr error
}

type SetDownstream struct{ PID *actor.PID }

type internalKafkaMsg struct {
	kafkaMsg    kafka.Message
	sourceTopic string
	err         error
}

type ReaderError struct {
	Topic             string
	ConsecutiveErrors int
	LastError         error
	BackoffDuration   time.Duration
}

type RestartReader struct {
	Topic string
}

type KafkaBlockEvent struct {
	BlockApply *struct {
		Height    uint64 `json:"height"`
		ID        string `json:"id"`
		Timestamp int64  `json:"timestamp"`
		NumTxs    int64  `json:"num_txs"`
	} `json:"BlockApply"`
	BlockUnapply *struct {
		Height    uint64 `json:"height"`
		ID        string `json:"id"`
		Timestamp int64  `json:"timestamp"`
		NumTxs    int64  `json:"num_txs"`
	} `json:"BlockUnapply"`
}

type KafkaTransactionEvent struct {
	AppliedEvent *struct {
		BlockId   string `json:"block_id"`
		Height    uint32 `json:"height"`
		Tx        string `json:"tx"`
		Timestamp int64  `json:"timestamp"`
	} `json:"AppliedEvent"`
	UnappliedEvent *struct {
		BlockId   string `json:"block_id"`
		Height    uint32 `json:"height"`
		Tx        string `json:"tx"`
		Timestamp int64  `json:"timestamp"`
	} `json:"UnappliedEvent"`
}

type KafkaMempoolEvent struct {
	TxAccepted *struct {
		Tx string `json:"tx"`
	} `json:"TxAccepted"`
	TxWithdrawn *struct {
		Tx        string `json:"tx"`
		Confirmed bool   `json:"confirmed"`
	} `json:"TxWithdrawn"`
}

type IndexerActor struct {
	cfg     *config.Config
	logger  *zap.SugaredLogger
	engine  *actor.Engine
	selfPID *actor.PID

	kafkaCtx      context.Context
	kafkaCancel   context.CancelFunc
	blockReader   *kafka.Reader
	txReader      *kafka.Reader
	mempoolReader *kafka.Reader

	downstreamPID     *actor.PID
	pendingDownstream []interface{} // buffers events until SetDownstream

	// per block buffers
	pendingApplied   map[blockKey][]txMessageWrapper
	pendingUnapplied map[blockKey][]txMessageWrapper

	// set of blocks we already emitted a BlockApply / BlockUnapply for
	emittedBlocks map[blockKey]struct{}

	// deduplication: track processed Kafka offsets per topic to prevent duplicate events
	// These maps use Kafka message offsets as idempotency keys to ensure that if Kafka
	// redelivers messages, we don't send duplicate transaction/mempool events downstream
	processedTxOffsets      map[int64]struct{}
	processedMempoolOffsets map[int64]struct{}

	tipHeight           uint64
	lastProcessedHeight uint64

	cursorLoaded bool

	completed map[uint64]*blockState

	db           *database.Database
	nodeProvider node.NodeProvider

	// tracks last known good offsets for reader restarts
	lastBlockOffset int64
	lastTxOffset    int64
}

func NewIndexerActor(
	cfg *config.Config,
	db *database.Database,
	nodeProvider node.NodeProvider,
	tipHeight uint64,
) actor.Producer {
	return func() actor.Receiver {
		return &IndexerActor{
			cfg: cfg,
			logger: logging.GetLogger().
				With("actor", "IndexerActor"),
			db:               db,
			nodeProvider:     nodeProvider,
			pendingApplied:   make(map[blockKey][]txMessageWrapper),
			pendingUnapplied: make(map[blockKey][]txMessageWrapper),

			emittedBlocks:           make(map[blockKey]struct{}),
			processedTxOffsets:      make(map[int64]struct{}),
			processedMempoolOffsets: make(map[int64]struct{}),
			completed:               make(map[uint64]*blockState),
			// Initialize to InterceptHeight - 1. This ensures that on a fresh start,
			// we process the block *at* InterceptHeight.
			lastProcessedHeight: func() uint64 {
				if cfg.Indexer.InterceptHeight > 0 {
					return cfg.Indexer.InterceptHeight - 1
				}
				return 0
			}(),
			tipHeight: tipHeight,
		}
	}
}

func (a *IndexerActor) Receive(c *actor.Context) {
	// Update buffer metrics on each message
	a.updateBufferMetrics()

	switch msg := c.Message().(type) {
	case actor.Initialized:
		a.engine, a.selfPID = c.Engine(), c.PID()

	case actor.Started:
		if err := a.startKafka(); err != nil {
			a.logger.Fatalw("failed to start kafka", "err", err)
		}

	case actor.Stopped:
		if a.kafkaCancel != nil {
			a.kafkaCancel()
		}
		_ = a.blockReader.Close()
		_ = a.txReader.Close()
		_ = a.mempoolReader.Close()

	case SetDownstream:
		a.downstreamPID = msg.PID
		// Flush any events that were produced before SetDownstream arrived
		for _, ev := range a.pendingDownstream {
			a.engine.Send(a.downstreamPID, ev)
		}
		a.pendingDownstream = nil

	case ReaderError:
		a.handleReaderError(msg)

	case RestartReader:
		a.handleRestartReader(msg)

	case internalKafkaMsg:
		a.dispatchKafkaMsg(msg)

	case types.GetStatus:
		c.Respond(types.Status{
			LastProcessedHeight: a.lastProcessedHeight,
			TipHeight:           a.tipHeight,
			PendingDownstream:   len(a.pendingDownstream),
			ReadersAlive:        a.areReadersHealthy(),
		})
	}
}

func (a *IndexerActor) updateBufferMetrics() {
	metrics.UpdateBufferSize("pending_applied", len(a.pendingApplied))
	metrics.UpdateBufferSize("pending_unapplied", len(a.pendingUnapplied))

	metrics.UpdateBufferSize("emitted_blocks", len(a.emittedBlocks))
	metrics.UpdateBufferSize("completed", len(a.completed))
	metrics.UpdateBufferSize("processed_tx_offsets", len(a.processedTxOffsets))
	metrics.UpdateBufferSize(
		"processed_mempool_offsets",
		len(a.processedMempoolOffsets),
	)
	metrics.UpdateBufferSize("pending_downstream", len(a.pendingDownstream))

	// Update height metrics
	metrics.UpdateHeightMetrics(a.lastProcessedHeight, a.tipHeight)
}

func (a *IndexerActor) startKafka() error {
	a.kafkaCtx, a.kafkaCancel = context.WithCancel(context.Background())

	rdCfg := func(topic string) kafka.ReaderConfig {
		return kafka.ReaderConfig{
			Brokers:   a.cfg.Kafka.Brokers,
			Topic:     topic,
			Partition: 0,
			MinBytes:  a.cfg.Kafka.MinBytes,
			MaxBytes:  a.cfg.Kafka.MaxBytes,
		}
	}

	a.blockReader = kafka.NewReader(rdCfg(a.cfg.Kafka.BlockTopic))
	a.txReader = kafka.NewReader(rdCfg(a.cfg.Kafka.TxTopic))
	a.mempoolReader = kafka.NewReader(rdCfg(a.cfg.Kafka.MempoolTopic))
	if err := a.mempoolReader.SetOffset(kafka.LastOffset); err != nil {
		return fmt.Errorf("failed to set mempool reader offset: %w", err)
	}

	if cps, err := a.db.GetCursorPoints(); err == nil && len(cps) > 0 {
		if err := a.seekToLiveCursor(cps); err != nil {
			a.logger.Warnw(
				"all cursor points failed, falling back to intercept height",
				"height",
				a.cfg.Indexer.InterceptHeight,
				"err",
				err,
			)
		}
	} else {
		a.logger.Infow("no cursor points found in DB, starting from intercept height", "height", a.cfg.Indexer.InterceptHeight)
	}

	go a.consumeReader(a.blockReader)
	go a.consumeReader(a.txReader)
	go a.consumeReader(a.mempoolReader)

	return nil
}

func (a *IndexerActor) seekToLiveCursor(cps []database.CursorPoint) error {
	for _, cp := range cps {
		if !a.blockExistsOnNode(hex.EncodeToString(cp.Hash)) {
			a.logger.Warnw("cursor point not found on node, skipping",
				"height", cp.Height, "hash", hex.EncodeToString(cp.Hash))
			continue
		}

		a.logger.Infow("attempting to seek to live cursor point",
			"height", cp.Height,
			"hash", hex.EncodeToString(cp.Hash),
			"block_offset", cp.BlockOffset,
			"tx_offset", cp.TxOffset,
			"tx_hash", hex.EncodeToString(cp.TxHash))

		// First try direct offset access
		if err := a.seekUsingStoredOffsets(cp); err == nil {
			a.logger.Infow(
				"successfully resumed from cursor point using stored offsets",
				"height",
				cp.Height,
				"hash",
				hex.EncodeToString(cp.Hash),
				"block_offset",
				cp.BlockOffset,
				"tx_offset",
				cp.TxOffset,
			)
			return nil
		} else {
			a.logger.Warnw("failed to seek using stored offsets, falling back to linear scan",
				"height", cp.Height,
				"hash", hex.EncodeToString(cp.Hash),
				"err", err)
		}

		// Fall back to linear scan if direct offset fails
		if err := a.seekToCursorPoints([]database.CursorPoint{cp}); err != nil {
			a.logger.Warnw(
				"failed to seek to cursor point using linear scan, trying next",
				"height", cp.Height,
				"hash", hex.EncodeToString(cp.Hash),
				"err", err,
			)
			continue
		}

		a.logger.Infow(
			"successfully resumed from cursor point using linear scan",
			"height",
			cp.Height,
			"hash",
			hex.EncodeToString(cp.Hash),
		)
		return nil
	}

	return fmt.Errorf("no valid cursor points found")
}

// seekUsingStoredOffsets attempts to seek using stored Kafka offsets
func (a *IndexerActor) seekUsingStoredOffsets(cp database.CursorPoint) error {
	// Set block reader to stored offset
	if err := a.blockReader.SetOffset(cp.BlockOffset); err != nil {
		return fmt.Errorf("failed to set block reader offset: %w", err)
	}

	// Verify block message at offset
	ctx, cancel := context.WithTimeout(a.kafkaCtx, DefaultKafkaTimeout)
	blockMsg, err := a.blockReader.ReadMessage(ctx)
	cancel()
	if err != nil {
		return fmt.Errorf("failed to read block message: %w", err)
	}

	var blockEvt KafkaBlockEvent
	if err := json.Unmarshal(blockMsg.Value, &blockEvt); err != nil {
		return fmt.Errorf("failed to decode block message: %w", err)
	}

	if blockEvt.BlockApply == nil {
		return fmt.Errorf("expected BlockApply event at offset")
	}

	if !strings.EqualFold(blockEvt.BlockApply.ID,

		hex.EncodeToString(cp.Hash)) {
		return fmt.Errorf("block hash mismatch at offset")
	}

	// Track last known good block offset - use current position since ReadMessage already advanced
	a.lastBlockOffset = blockMsg.Offset

	// Set tx reader to stored offset if we have a tx hash
	if len(cp.TxHash) > 0 {
		if err := a.txReader.SetOffset(cp.TxOffset); err != nil {
			return fmt.Errorf("failed to set tx reader offset: %w", err)
		}

		// Verify tx message at offset
		ctx, cancel = context.WithTimeout(a.kafkaCtx, DefaultKafkaTimeout)
		txMsg, err := a.txReader.ReadMessage(ctx)
		cancel()
		if err != nil {
			return fmt.Errorf("failed to read tx message: %w", err)
		}

		var txEvt KafkaTransactionEvent
		if err := json.Unmarshal(txMsg.Value, &txEvt); err != nil {
			return fmt.Errorf("failed to decode tx message: %w", err)
		}

		if txEvt.AppliedEvent == nil {
			return fmt.Errorf("expected AppliedEvent at tx offset")
		}

		parsedTx, err := types.ParseCBORTransaction(txEvt.AppliedEvent.Tx)
		if err != nil {
			return fmt.Errorf("failed to parse tx: %w", err)
		}

		txHashBytes, err := hex.DecodeString(parsedTx.ID)
		if err != nil {
			return fmt.Errorf("failed to decode tx hash: %w", err)
		}

		if !bytes.Equal(txHashBytes, cp.TxHash) {
			return fmt.Errorf("tx hash mismatch at offset")
		}

		a.logger.Infow("verified transaction hash at offset",
			"tx_hash", hex.EncodeToString(cp.TxHash),
			"tx_offset", cp.TxOffset)

		// Track last known good tx offset and advance past the verified message
		a.lastTxOffset = cp.TxOffset + 1
		if err := a.txReader.SetOffset(a.lastTxOffset); err != nil {
			return fmt.Errorf("failed to advance tx reader: %w", err)
		}
	}

	a.lastProcessedHeight = cp.Height
	a.cursorLoaded = true

	nodeInfo, err := a.nodeProvider.GetNodeInfo(context.Background())
	if err != nil {
		a.logger.Warnw(
			"failed to get node info after cursor restore",
			"err",
			err,
		)
	} else {
		a.tipHeight = uint64(*nodeInfo.FullHeight)
		a.logger.Infow("updated tip height after cursor restore",
			"tip_height", a.tipHeight,
			"restored_height", cp.Height)
	}

	return nil
}

func (a *IndexerActor) blockExistsOnNode(hash string) bool {
	_, err := a.nodeProvider.GetBlockHeaderByID(
		context.Background(),
		node.ModifierID(hash),
	)
	if err != nil {
		a.logger.Warnw("node check failed", "hash", hash, "err", err)
		return false
	}
	return true
}

func (a *IndexerActor) seekToCursorPoints(cps []database.CursorPoint) error {
	for i, cp := range cps {
		a.logger.Infow(
			"trying cursor point",
			"height",
			cp.Height,
			"hash",
			hex.EncodeToString(cp.Hash),
			"attempt",
			i+1,
			"total",
			len(cps),
		)

		targetHash := strings.ToLower(hex.EncodeToString(cp.Hash))
		targetHeight := cp.Height

		if err := a.blockReader.SetOffset(kafka.FirstOffset); err != nil {
			return err
		}

		var tgt int64 = -1
		found := false

		for {
			ctx, cancel := context.WithTimeout(a.kafkaCtx, DefaultKafkaTimeout)
			msg, err := a.blockReader.ReadMessage(ctx)
			cancel()
			if err != nil {
				if errors.Is(err, context.DeadlineExceeded) ||
					errors.Is(err, io.EOF) {
					a.logger.Warnw(
						"cursor point not found in kafka",
						"height",
						targetHeight,
						"hash",
						targetHash,
					)
					break
				}
				return fmt.Errorf("cursor scan failed: %w", err)
			}

			var evt KafkaBlockEvent
			if json.Unmarshal(msg.Value, &evt) != nil || evt.BlockApply == nil {
				continue
			}

			if strings.ToLower(evt.BlockApply.ID) == targetHash &&
				evt.BlockApply.Height == targetHeight {
				tgt = msg.Offset + 1
				found = true
				a.logger.Infow(
					"found cursor point in kafka",
					"height",
					targetHeight,
					"hash",
					targetHash,
					"offset",
					tgt,
				)
				break
			}
		}

		if found {
			// Set ONLY the block reader offset. The transaction reader will catch up.
			// Offsets are NOT correlated across topics.
			if err := a.blockReader.SetOffset(tgt); err != nil {
				return err
			}

			a.lastProcessedHeight = targetHeight
			a.cursorLoaded = true
			a.logger.Infow(
				"successfully resumed from cursor",
				"height",
				targetHeight,
				"hash",
				targetHash,
				"offset",
				tgt,
				"lastProcessedHeight",
				a.lastProcessedHeight,
			)
			return nil
		}

		a.logger.Warnw(
			"cursor point not found, trying next older cursor",
			"height",
			targetHeight,
			"hash",
			targetHash,
		)
	}

	a.logger.Warnw(
		"no valid cursor points found in kafka - starting from configured intercept height",
	)
	return nil
}

func (a *IndexerActor) consumeReader(r *kafka.Reader) {
	backoff := DefaultBackoffInitial
	consecutiveErrors := 0

	for {
		msg, err := r.FetchMessage(a.kafkaCtx)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, io.EOF) {
				return
			}

			consecutiveErrors++
			wrappedErr := fmt.Errorf("fetch failed: %w", err)
			a.logger.Error("reader error",
				zap.String("topic", r.Config().Topic),
				zap.Int("consecutiveErrors", consecutiveErrors),
				zap.Error(wrappedErr),
			)

			// Update lag metrics even on error
			lag, err := r.ReadLag(a.kafkaCtx)
			if err == nil {
				metrics.RecordKafkaLag(r.Config().Topic, 0, lag)
			}

			if consecutiveErrors >= ErrorReportThreshold {
				a.engine.Send(a.selfPID, ReaderError{
					Topic:             r.Config().Topic,
					ConsecutiveErrors: consecutiveErrors,
					LastError:         err,
					BackoffDuration:   backoff,
				})
			}

			time.Sleep(backoff)
			if backoff < MaxBackoff {
				backoff *= 2
			}
			continue
		}

		// Update lag metrics on successful fetch
		lag, err := r.ReadLag(a.kafkaCtx)
		if err == nil {
			metrics.RecordKafkaLag(r.Config().Topic, 0, lag)
		}

		// reset failure tracking on successful fetch
		if consecutiveErrors > 0 {
			a.logger.Info(
				"reader recovered",
				zap.String("topic", r.Config().Topic),
				zap.Int("afterErrors", consecutiveErrors),
			)
			consecutiveErrors = 0
		}
		backoff = DefaultBackoffInitial
		a.engine.Send(a.selfPID, internalKafkaMsg{msg, r.Config().Topic, nil})
	}
}

func (a *IndexerActor) dispatchKafkaMsg(m internalKafkaMsg) {
	switch m.sourceTopic {
	case a.cfg.Kafka.BlockTopic:
		a.processBlockMessage(m.kafkaMsg)
	case a.cfg.Kafka.TxTopic:
		a.bufferOrProcessTx(m.kafkaMsg)
	case a.cfg.Kafka.MempoolTopic:
		a.processMempoolMessage(m.kafkaMsg)
	default:
		a.logger.Warnw("unknown kafka topic", "topic", m.sourceTopic)
	}
}

func (a *IndexerActor) handleReaderError(msg ReaderError) {
	err := fmt.Errorf("reader error: %w", msg.LastError)
	a.logger.Error("reader experiencing persistent failures",
		zap.String("topic", msg.Topic),
		zap.Int("consecutiveErrors", msg.ConsecutiveErrors),
		zap.Error(err),
		zap.Duration("backoffDuration", msg.BackoffDuration),
	)

	switch {
	case msg.ConsecutiveErrors >= CriticalErrorThreshold:
		a.logger.Warn(
			"critical reader failure, attempting restart",
			zap.String("topic", msg.Topic),
		)
		a.engine.Send(a.selfPID, RestartReader{Topic: msg.Topic})

	case msg.ConsecutiveErrors >= WarningErrorThreshold:
		a.logger.Warn(
			"persistent reader issues detected",
			zap.String("topic", msg.Topic),
			zap.Int("consecutiveErrors", msg.ConsecutiveErrors),
		)

	default:
		a.logger.Info(
			"reader error reported",
			zap.String("topic", msg.Topic),
			zap.Int("consecutiveErrors", msg.ConsecutiveErrors),
		)
	}
}

func (a *IndexerActor) handleRestartReader(msg RestartReader) {
	a.logger.Warn("restarting reader", zap.String("topic", msg.Topic))
	metrics.RecordReaderRestart(msg.Topic)

	rdCfg := func(topic string) kafka.ReaderConfig {
		return kafka.ReaderConfig{
			Brokers:   a.cfg.Kafka.Brokers,
			Topic:     topic,
			Partition: 0,
			MinBytes:  a.cfg.Kafka.MinBytes,
			MaxBytes:  a.cfg.Kafka.MaxBytes,
		}
	}

	switch msg.Topic {
	case a.cfg.Kafka.BlockTopic:
		if a.blockReader != nil {
			_ = a.blockReader.Close()
		}
		a.blockReader = kafka.NewReader(rdCfg(msg.Topic))
		// Restore last known good offset if available
		if a.lastBlockOffset > 0 {
			if err := a.blockReader.SetOffset(a.lastBlockOffset); err != nil {
				a.logger.Errorw("failed to restore block reader offset",
					"offset", a.lastBlockOffset,
					"err", err)
			} else {
				a.logger.Infow("restored block reader offset",
					"offset", a.lastBlockOffset)
			}
		}
		go a.consumeReader(a.blockReader)
		a.logger.Info("block reader restarted", zap.String("topic", msg.Topic))

	case a.cfg.Kafka.TxTopic:
		if a.txReader != nil {
			_ = a.txReader.Close()
		}
		a.txReader = kafka.NewReader(rdCfg(msg.Topic))
		// Restore last known good offset if available
		if a.lastTxOffset > 0 {
			if err := a.txReader.SetOffset(a.lastTxOffset); err != nil {
				a.logger.Errorw("failed to restore tx reader offset",
					"offset", a.lastTxOffset,
					"err", err)
			} else {
				a.logger.Infow("restored tx reader offset",
					"offset", a.lastTxOffset)
			}
		}
		go a.consumeReader(a.txReader)
		a.logger.Info("transaction reader restarted",
			zap.String("topic", msg.Topic))

	case a.cfg.Kafka.MempoolTopic:
		if a.mempoolReader != nil {
			_ = a.mempoolReader.Close()
		}
		a.mempoolReader = kafka.NewReader(rdCfg(msg.Topic))
		if err := a.mempoolReader.SetOffset(kafka.LastOffset); err != nil {
			a.logger.Error(
				"failed to set mempool reader offset",
				zap.Error(err),
			)
		}
		go a.consumeReader(a.mempoolReader)
		a.logger.Info("mempool reader restarted",
			zap.String("topic", msg.Topic))

	default:
		a.logger.Error(
			"unknown topic for restart",
			zap.String("topic", msg.Topic),
		)
	}
}

func (a *IndexerActor) processBlockMessage(km kafka.Message) {
	var evt KafkaBlockEvent
	if err := json.Unmarshal(km.Value, &evt); err != nil {
		metrics.RecordDecodeFailure(a.cfg.Kafka.BlockTopic, "block")
		a.logger.Error("block decode failed", zap.Error(err))
		return
	}

	switch {
	case evt.BlockApply != nil:
		a.logger.Debugw(
			"processing block apply message",
			"block",
			evt.BlockApply,
		)
		key := blockKey{
			Height: evt.BlockApply.Height,
			Hash:   strings.ToLower(evt.BlockApply.ID),
		}
		a.emitBlockApply(
			key,
			evt.BlockApply.Timestamp,
			evt.BlockApply.NumTxs,
			km.Offset,
		)

	case evt.BlockUnapply != nil:
		a.logger.Debugw(
			"processing block unapply message",
			"block",
			evt.BlockUnapply,
		)
		key := blockKey{
			Height: evt.BlockUnapply.Height,
			Hash:   strings.ToLower(evt.BlockUnapply.ID),
		}
		a.emitBlockUnapply(key, evt.BlockUnapply.NumTxs)
	}
}

func (a *IndexerActor) emitBlockApply(
	key blockKey,
	ts int64,
	numTxs int64,
	blockOffset int64,
) {
	if _, done := a.emittedBlocks[key]; done {
		// return since its a duplicate
		return
	}

	if key.Height >= a.tipHeight {
		nodeInfo, err := a.nodeProvider.GetNodeInfo(context.Background())
		if err != nil {
			a.logger.Errorw("failed to get node info", "err", err)
			return
		}
		a.tipHeight = uint64(*nodeInfo.FullHeight)
	}
	a.sendDownstream(types.IndexerBlockEvent{
		BlockNumber: key.Height,
		BlockHash:   key.Hash,
		Timestamp:   ts,
		TipReached:  a.tipHeight == key.Height,
		NumTxs:      numTxs,
		BlockOffset: blockOffset, // may be 0 for historical blocks restored via seekToCursorPoints
	})
	a.emittedBlocks[key] = struct{}{}

	st := &blockState{expected: numTxs}
	a.completed[key.Height] = st

	// Process any buffered transactions for this block
	if txs := a.pendingApplied[key]; len(txs) > 0 {
		for _, m := range txs {
			a.processTransactionMessage(m)
		}
		delete(a.pendingApplied, key)
	}
	if txs := a.pendingUnapplied[key]; len(txs) > 0 {
		for _, m := range txs {
			a.processTransactionMessage(m)
		}
		delete(a.pendingUnapplied, key)
	}

	if numTxs == 0 {
		a.tryAdvanceCursor()
	}

	a.pruneOldBuffers()
}

func (a *IndexerActor) emitBlockUnapply(key blockKey, numTxs int64) {
	delete(a.emittedBlocks, key)
	a.sendDownstream(
		types.IndexerRollbackEvent{
			Height:    key.Height,
			BlockHash: key.Hash,
			NumTxs:    numTxs,
		},
	)

	if st, ok := a.completed[key.Height]; ok {
		st.rollback = true
	} else {
		a.completed[key.Height] = &blockState{expected: numTxs, rollback: true}
	}

	if txs := a.pendingUnapplied[key]; len(txs) > 0 {
		for _, m := range txs {
			a.processTransactionMessage(m)
		}
		delete(a.pendingUnapplied, key)
	}

	a.tryAdvanceCursor()
	a.pruneOldBuffers()
}

func (a *IndexerActor) bufferOrProcessTx(km kafka.Message) {
	if _, processed := a.processedTxOffsets[km.Offset]; processed {
		a.logger.Debugw(
			"skipping duplicate transaction message",
			"offset",
			km.Offset,
		)
		return
	}

	var evt KafkaTransactionEvent
	if err := json.Unmarshal(km.Value, &evt); err != nil {
		metrics.RecordDecodeFailure(a.cfg.Kafka.TxTopic, "transaction")
		a.logger.Error("tx decode failed", zap.Error(err))
		return
	}

	// mark processed
	a.processedTxOffsets[km.Offset] = struct{}{}

	wrapper := txMessageWrapper{kafkaMsg: km}

	switch {
	case evt.AppliedEvent != nil:
		a.logger.Debugw(
			"processing transaction message",
			"transaction",
			evt.AppliedEvent,
		)
		height := uint64(evt.AppliedEvent.Height)

		// Skip check: On fresh start, skip blocks before InterceptHeight
		// On resume, skip transactions at or below last processed height
		skip := (a.cursorLoaded && height <= a.lastProcessedHeight) ||
			(!a.cursorLoaded && height < a.cfg.Indexer.InterceptHeight)

		if skip {
			return
		}

		key := blockKey{
			Height: height,
			Hash:   strings.ToLower(evt.AppliedEvent.BlockId),
		}

		wrapper.parsedTx, wrapper.parseErr = types.ParseCBORTransaction(
			evt.AppliedEvent.Tx,
		)
		if wrapper.parseErr != nil {
			a.logger.Warnw(
				"failed to parse transaction for ID",
				"height",
				key.Height,
				"err",
				wrapper.parseErr,
			)
		}

		if _, done := a.emittedBlocks[key]; done {
			a.processTransactionMessage(wrapper)
		} else {
			a.pendingApplied[key] = append(a.pendingApplied[key], wrapper)
		}

	case evt.UnappliedEvent != nil:
		key := blockKey{
			Height: uint64(evt.UnappliedEvent.Height),
			Hash:   strings.ToLower(evt.UnappliedEvent.BlockId),
		}

		// Parse transaction once
		wrapper.parsedTx, wrapper.parseErr = types.ParseCBORTransaction(
			evt.UnappliedEvent.Tx,
		)
		if wrapper.parseErr != nil {
			a.logger.Warnw(
				"failed to parse transaction for ID in UnappliedEvent",
				"err",
				wrapper.parseErr,
			)
			return
		}

		if _, done := a.emittedBlocks[key]; done {
			a.processTransactionMessage(wrapper)
		} else {
			a.pendingUnapplied[key] = append(a.pendingUnapplied[key], wrapper)
		}
	}
}

func (a *IndexerActor) processTransactionMessage(wrapper txMessageWrapper) {
	var txEvt KafkaTransactionEvent
	if err := json.Unmarshal(wrapper.kafkaMsg.Value, &txEvt); err != nil {
		metrics.RecordDecodeFailure(a.cfg.Kafka.TxTopic, "transaction")
		a.logger.Error("transaction decode failed", zap.Error(err))
		return
	}

	switch {
	case txEvt.AppliedEvent != nil:
		h := uint64(txEvt.AppliedEvent.Height)
		if st, ok := a.completed[h]; ok {
			st.seen++
			a.tryAdvanceCursor()
		}

		a.sendDownstream(types.IndexerTransactionEvent{
			Transaction:    txEvt.AppliedEvent.Tx,
			ParsedTx:       wrapper.parsedTx,
			Height:         h,
			EventTimestamp: time.Unix(txEvt.AppliedEvent.Timestamp, 0),
			TxOffset:       wrapper.kafkaMsg.Offset,
		})

	case txEvt.UnappliedEvent != nil:
		h := uint64(txEvt.UnappliedEvent.Height)
		if st, ok := a.completed[h]; ok {
			st.rollback = true
			a.tryAdvanceCursor()
		}

		a.sendDownstream(types.IndexerTransactionUnappliedEvent{
			Transaction: txEvt.UnappliedEvent.Tx,
			ParsedTx:    wrapper.parsedTx,
		})
	}
}

func (a *IndexerActor) processMempoolMessage(km kafka.Message) {
	if a.lastProcessedHeight < a.tipHeight {
		a.logger.Debug(
			"dropping mempool event - not at tip yet",
			zap.Uint64("current_height", a.lastProcessedHeight),
			zap.Uint64("tip_height", a.tipHeight),
		)
		return
	}

	if _, processed := a.processedMempoolOffsets[km.Offset]; processed {
		a.logger.Debug(
			"skipping duplicate mempool message",
			zap.Int64("offset", km.Offset),
		)
		return
	}

	var evt KafkaMempoolEvent
	if err := json.Unmarshal(km.Value, &evt); err != nil {
		metrics.RecordDecodeFailure(a.cfg.Kafka.MempoolTopic, "mempool")
		a.logger.Error("mempool decode failed", zap.Error(err))
		return
	}

	// mark processed
	a.processedMempoolOffsets[km.Offset] = struct{}{}

	if evt.TxAccepted != nil {
		parsedTx, err := types.ParseCBORTransaction(evt.TxAccepted.Tx)
		if err != nil {
			a.logger.Errorw("failed to decode CBOR mempool transaction",
				"error", err,
				"event_type", "accepted",
				"tx", evt.TxAccepted.Tx)
			parsedTx = nil
		}

		a.sendDownstream(
			types.IndexerMempoolAcceptedEvent{
				Transaction: evt.TxAccepted.Tx,
				ParsedTx:    parsedTx,
			},
		)
	} else if evt.TxWithdrawn != nil {
		parsedTx, err := types.ParseCBORTransaction(evt.TxWithdrawn.Tx)
		if err != nil {
			a.logger.Errorw("failed to decode CBOR mempool transaction",
				"error", err,
				"event_type", "withdrawn",
				"tx", evt.TxWithdrawn.Tx)
			parsedTx = nil
		}

		a.sendDownstream(types.IndexerMempoolWithdrawnEvent{
			Transaction: evt.TxWithdrawn.Tx,
			ParsedTx:    parsedTx,
			Confirmed:   evt.TxWithdrawn.Confirmed,
		})
	}
}

func (a *IndexerActor) pruneOldBuffers() {
	if a.lastProcessedHeight < a.tipHeight {
		return
	}

	maxLookback := DefaultMaxStragglersLookback
	if a.cfg.Kafka.MaxStragglersLookback > 0 {
		maxLookback = a.cfg.Kafka.MaxStragglersLookback
	}

	drop := func(m map[blockKey][]txMessageWrapper) {
		for key := range m {
			if key.Height > 0 && a.lastProcessedHeight > key.Height &&
				a.lastProcessedHeight-key.Height > uint64(maxLookback) {
				delete(m, key)
			}
		}
	}
	drop(a.pendingApplied)
	drop(a.pendingUnapplied)

	for key := range a.emittedBlocks {
		if key.Height > 0 && a.lastProcessedHeight > key.Height &&
			a.lastProcessedHeight-key.Height > uint64(maxLookback) {
			delete(a.emittedBlocks, key)
		}
	}
	for h := range a.completed {
		if h > 0 && a.lastProcessedHeight > h &&
			a.lastProcessedHeight-h > uint64(maxLookback) {
			delete(a.completed, h)
		}
	}

	//TODO: improve this pruning logic
	// Prune processed offset maps to prevent memory leaks
	// Keep a reasonable buffer of recent offsets for deduplication
	// Since we don't have direct height->offset mapping, we'll use a simple size-based pruning
	const maxOffsetEntries = MaxOffsetEntries // Adjust based on expected message volume

	if len(a.processedTxOffsets) > maxOffsetEntries {
		// Find the oldest N offsets to remove (keep the newest ones)
		offsets := make([]int64, 0, len(a.processedTxOffsets))
		for offset := range a.processedTxOffsets {
			offsets = append(offsets, offset)
		}
		// Sort to identify oldest offsets (assuming offsets are generally increasing)
		sort.Slice(
			offsets,
			func(i, j int) bool { return offsets[i] < offsets[j] },
		)
		// Remove oldest half
		removeCount := len(offsets) / 2
		for i := 0; i < removeCount; i++ {
			delete(a.processedTxOffsets, offsets[i])
		}
		a.logger.Infow(
			"pruned old transaction offsets",
			"removed",
			removeCount,
			"remaining",
			len(a.processedTxOffsets),
		)
	}

	if len(a.processedMempoolOffsets) > maxOffsetEntries {
		// Same logic for mempool offsets
		offsets := make([]int64, 0, len(a.processedMempoolOffsets))
		for offset := range a.processedMempoolOffsets {
			offsets = append(offsets, offset)
		}
		sort.Slice(
			offsets,
			func(i, j int) bool { return offsets[i] < offsets[j] },
		)
		removeCount := len(offsets) / 2
		for i := 0; i < removeCount; i++ {
			delete(a.processedMempoolOffsets, offsets[i])
		}
		a.logger.Infow(
			"pruned old mempool offsets",
			"removed",
			removeCount,
			"remaining",
			len(a.processedMempoolOffsets),
		)
	}
}

func (a *IndexerActor) tryAdvanceCursor() {
	nextHeight := a.lastProcessedHeight + 1
	for st, ok := a.completed[nextHeight]; ok; nextHeight++ {
		// block considered "done" if it was rolled back or if we have seen all its expected transactions.
		done := st.rollback || (st.seen == st.expected)

		if !done {
			return
		}

		a.lastProcessedHeight = nextHeight
		a.logger.Debugw(
			"cursor advanced",
			"height",
			nextHeight,
			"reason",
			func() string {
				if st.rollback {
					return "rollback"
				}
				return fmt.Sprintf("complete (%d/%d txs)", st.seen, st.expected)
			}(),
		)

		delete(a.completed, nextHeight)
		st, ok = a.completed[nextHeight+1]
	}
}

func (a *IndexerActor) sendDownstream(v interface{}) {
	if a.downstreamPID == nil {
		a.pendingDownstream = append(a.pendingDownstream, v)
		return
	}
	a.engine.Send(a.downstreamPID, v)
}

func (a *IndexerActor) areReadersHealthy() bool {
	const healthyThreshold = ErrorReportThreshold

	for _, reader := range []*kafka.Reader{a.blockReader, a.txReader, a.mempoolReader} {
		if reader == nil {
			return false
		}
		stats := reader.Stats()
		if stats.Errors >= healthyThreshold {
			return false
		}
	}
	return true
}
