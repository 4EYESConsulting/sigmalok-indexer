package strategy

import "time"

// MempoolTxInfo tracks mempool transactions and their confirmation status
type MempoolTxInfo struct {
	TxHash          string
	Action          int
	SeenAtTipHeight uint64
	AddedAt         time.Time
}

// addMempoolTx adds a transaction to the mempool tracking map
func (s *ChainEventProcessorActor) addMempoolTx(
	txHash string,
	action int,
	tipHeight uint64,
) {
	s.mempoolTxMap[txHash] = &MempoolTxInfo{
		TxHash:          txHash,
		Action:          action,
		SeenAtTipHeight: tipHeight,
		AddedAt:         time.Now(),
	}
}

// garbageCollectMempoolTxs removes stale mempool transactions
func (s *ChainEventProcessorActor) garbageCollectMempoolTxs(
	currentHeight uint64,
) {
	toDelete := []string{}

	for txHash, mempoolInfo := range s.mempoolTxMap {
		if time.Since(
			mempoolInfo.AddedAt,
		) > s.appCfg.Indexer.MempoolTxExpirationTime {
			toDelete = append(toDelete, txHash)
		}
	}

	for _, txHash := range toDelete {
		mempoolInfo := s.mempoolTxMap[txHash]
		switch mempoolInfo.Action {
		case GenesisAction:
			if err := s.db.RollbackGenesisByTxID(txHash); err != nil {
				s.logger.Errorw("failed to rollback genesis by tx id",
					"tx_hash", txHash,
					"error", err)
			}
		case CreateTokenLokAction:
			if err := s.db.RollbackMintByTxID(txHash); err != nil {
				s.logger.Errorw("failed to rollback mint by tx id",
					"tx_hash", txHash,
					"error", err)
			}
		case FundLokAction:
			if err := s.db.RollbackFundingByTxID(txHash); err != nil {
				s.logger.Errorw("failed to rollback funding by tx id",
					"tx_hash", txHash,
					"error", err)
			}
		case RedeemLokAction:
			if err := s.db.RollbackRedeemByTxID(txHash); err != nil {
				s.logger.Errorw("failed to rollback redeem by tx id",
					"tx_hash", txHash,
					"error", err)
			}
		}
		delete(s.mempoolTxMap, txHash)
		s.logger.Infow("garbage collected mempool transaction",
			"tx_hash", txHash,
			"action", mempoolInfo.Action,
			"seen_at_tip_height", mempoolInfo.SeenAtTipHeight,
			"current_height", currentHeight)
	}

	if len(toDelete) > 0 {
		s.logger.Infow("garbage collection completed",
			"removed_count", len(toDelete),
			"remaining_count", len(s.mempoolTxMap))
	}
}
