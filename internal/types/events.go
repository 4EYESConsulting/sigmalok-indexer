package types

import (
	"time"

	"github.com/anthdm/hollywood/actor"
)

// Strategy configuration
type StrategyConfig struct {
	ID               string `json:"id"`
	Kind             string `json:"kind"`
	StartBlockHeight uint64 `json:"start_block_height"`
	StartBlockHash   string `json:"start_block_hash"`
}

// Message to set parent PID for strategy actors
type SetParentPID struct {
	PID *actor.PID
}

// Events that can be sent to strategies

// IndexerBlockEvent represents a new block event from Ergo blockchain
type IndexerBlockEvent struct {
	BlockNumber uint64 `json:"block_number"`
	BlockHash   string `json:"block_hash"`
	Timestamp   int64  `json:"timestamp"`
	TipReached  bool   `json:"tip_reached"`
	NumTxs      int64  `json:"num_txs"`
	BlockOffset int64  `json:"block_offset"`
}

// IndexerTransactionEvent represents a transaction event from Ergo blockchain
type IndexerTransactionEvent struct {
	Transaction    string       `json:"transaction"` // Base64 CBOR encoded transaction (for backward compatibility)
	ParsedTx       *Transaction `json:"parsed_tx"`   // Decoded transaction object
	Height         uint64       `json:"height"`      // Block height
	EventTimestamp time.Time    `json:"event_timestamp"`
	TxOffset       int64        `json:"tx_offset"`
}

// IndexerTransactionUnappliedEvent represents an unapplied transaction event
// This type doesn't include height or timestamp since they're not available for unapplied events
type IndexerTransactionUnappliedEvent struct {
	Transaction string       `json:"transaction"` // Base64 CBOR encoded transaction (for backward compatibility)
	ParsedTx    *Transaction `json:"parsed_tx"`   // Decoded transaction object
}

// IndexerRollbackEvent represents a rollback event
type IndexerRollbackEvent struct {
	Height    uint64 `json:"height"`
	BlockHash string `json:"block_hash"`
	NumTxs    int64  `json:"num_txs"`
}

// IndexerMempoolEvent represents a mempool transaction event
type IndexerMempoolEvent struct {
	Transaction string       `json:"transaction"` // Base64 CBOR encoded transaction (for backward compatibility)
	ParsedTx    *Transaction `json:"parsed_tx"`   // Decoded transaction object
	EventType   string       `json:"event_type"`  // "accepted" or "withdrawn"
}

// IndexerMempoolAcceptedEvent represents a transaction accepted into the mempool
type IndexerMempoolAcceptedEvent struct {
	Transaction string       `json:"transaction"` // Base64 CBOR encoded transaction (for backward compatibility)
	ParsedTx    *Transaction `json:"parsed_tx"`   // Decoded transaction object
}

// IndexerMempoolWithdrawnEvent represents a transaction withdrawn from the mempool
type IndexerMempoolWithdrawnEvent struct {
	Transaction string       `json:"transaction"` // Base64 CBOR encoded transaction (for backward compatibility)
	ParsedTx    *Transaction `json:"parsed_tx"`   // Decoded transaction object
	Confirmed   bool         `json:"confirmed"`   // True if the transaction was confirmed
}

// IndexerStatusEvent represents status updates from the indexer
type IndexerStatusEvent struct {
	CursorHeight uint64 `json:"cursor_height"`
	CursorHash   string `json:"cursor_hash"`
	Height       uint64 `json:"height"`
	TipHeight    uint64 `json:"tip_height"`
	TipHash      string `json:"tip_hash"`
	TipReached   bool   `json:"tip_reached"`
	IsRollback   bool   `json:"is_rollback"`
}

// IndexerRestartRequest requests a restart of the indexer
type IndexerRestartRequest struct {
	Reason        string     `json:"reason"`
	StrategyPID   *actor.PID `json:"-"`              // PID of the strategy that triggered the restart
	RestartHeight uint64     `json:"restart_height"` // Slot to restart from
}

// IndexerRestartComplete indicates completion of an indexer restart
type IndexerRestartComplete struct {
	Success bool   `json:"success"`
	Error   string `json:"error"`
}
