package strategy

import (
	"4EYESConsulting/sigmalok-indexer/internal/types"
	"errors"
	"fmt"

	ergo "github.com/sigmaspace-io/ergo-lib-go"
)

func (s *ChainEventProcessorActor) handleGenesis(
	ctx TxContext,
	tx *types.Transaction,
	lokBox ergo.Box,
	regs *LokBoxRegisters,
	singleton string,
) error {
	txExists, err := s.db.CheckGenesisTxExists(tx.ID)
	if err != nil {
		return fmt.Errorf("failed to check if genesis tx exists: %w", err)
	}

	if !ctx.Confirmed {
		if txExists {
			s.logger.Infof(
				"mempool genesis transaction %s already exists in database, skipping",
				tx.ID,
			)
			return nil
		}

		singletonExists, isConfirmed, err := s.db.CheckSingletonExists(
			singleton,
		)
		if err != nil {
			return fmt.Errorf("failed to check if singleton exists: %w", err)
		}

		if singletonExists {
			if isConfirmed {
				s.logger.Infof(
					"Singleton %s already exists and is confirmed, ignoring mempool transaction %s",
					singleton,
					tx.ID,
				)
				return nil
			} else {
				s.logger.Infof("Singleton %s exists but is unconfirmed, replacing with new mempool transaction", singleton)
				existingTx, err := s.db.GetGenesisTransactionBySingleton(singleton)
				if err != nil {
					return fmt.Errorf("failed to get existing genesis transaction: %w", err)
				}
				if err := s.db.RollbackGenesisByTxID(existingTx); err != nil {
					return fmt.Errorf("failed to delete unconfirmed contract: %w", err)
				}
			}
		}

		return s.writeGenesis(ctx, tx, lokBox, regs, singleton)
	}

	if txExists {
		unconfirmed, err := s.db.CheckGenesisTxExistsAndUnconfirmed(tx.ID)
		if err != nil {
			return fmt.Errorf(
				"failed to check if genesis tx is unconfirmed: %w",
				err,
			)
		}
		if unconfirmed {
			s.logger.Infow(
				"updating unconfirmed genesis transaction to confirmed",
				"tx_hash",
				tx.ID,
			)
			if err := s.db.UpdateGenesisTxToConfirmed(tx.ID, ctx.BlockHeight, ctx.BlockHash, ctx.Timestamp); err != nil {
				return fmt.Errorf(
					"failed to update genesis transaction to confirmed: %w",
					err,
				)
			}

			delete(s.mempoolTxMap, tx.ID)

			return nil
		}
		s.logger.Warnf(
			"[THIS SHOULD NOT HAPPEN] Genesis transaction %s already exists and is confirmed, skipping",
			tx.ID,
		)
		return nil
	}

	return s.writeGenesis(ctx, tx, lokBox, regs, singleton)
}

func (s *ChainEventProcessorActor) writeGenesis(
	ctx TxContext,
	tx *types.Transaction,
	lokBox ergo.Box,
	regs *LokBoxRegisters,
	singleton string,
) error {
	tokenEvents := makeTokenEvents(lokBox, nil)

	creatorBase58, err := decodePkHexToBase58(tx.Inputs[0].ErgoTree)
	if err != nil {
		return fmt.Errorf("failed to decode creator public key: %w", err)
	}

	benefactorBase58, err := decodePkHexToBase58(regs.R4)
	if err != nil {
		return fmt.Errorf("failed to decode benefactor public key: %w", err)
	}

	erg := uint64(lokBox.BoxValue().Int64())
	deadline := uint64(regs.R6Long1)
	name := regs.R9Colls[0][0]
	oracleNFT := regs.R8Bytes
	var oracleNFTValue *string
	if oracleNFT == "" {
		oracleNFTValue = nil
	} else {
		oracleNFTValue = &oracleNFT
	}

	var oracleValue *uint64
	var oracleIsGreaterThan *bool
	if oracleNFTValue != nil {
		val := uint64(regs.R6Long2)
		oracleValue = &val
		oracleIsGreaterThan = &regs.R8Bool
	}

	feeAddressHash := regs.R9Colls[0][1]
	feeAmount := int(regs.R9Long)

	s.logger.Infow("Writing genesis transaction to database",
		"tx_hash", tx.ID,
		"singleton", singleton,
		"confirmed", ctx.Confirmed)

	if ctx.Confirmed {
		return s.db.WriteGenesisTx(
			singleton,
			ctx.BlockHeight,
			ctx.BlockHash,
			ctx.Timestamp,
			&name,
			creatorBase58,
			benefactorBase58,
			erg,
			deadline,
			oracleNFTValue,
			oracleValue,
			oracleIsGreaterThan,
			tx.ID,
			feeAddressHash,
			feeAmount,
			true,
			tokenEvents,
		)
	}

	return s.db.WriteGenesisTxMempool(
		ctx.BlockHeight,
		ctx.BlockHash,
		singleton,
		&name,
		creatorBase58,
		benefactorBase58,
		erg,
		deadline,
		oracleNFTValue,
		oracleValue,
		oracleIsGreaterThan,
		tx.ID,
		feeAddressHash,
		feeAmount,
		tokenEvents,
	)
}

func (s *ChainEventProcessorActor) handleMint(
	ctx TxContext,
	tx *types.Transaction,
	lokBox ergo.Box,
	regs *LokBoxRegisters,
	singleton string,
) error {
	txExists, err := s.db.CheckMintTxExists(tx.ID)
	if err != nil {
		return fmt.Errorf("failed to check if mint tx exists: %w", err)
	}

	if !ctx.Confirmed {
		if txExists {
			s.logger.Infof(
				"mempool mint transaction %s already exists in database, skipping",
				tx.ID,
			)
			return nil
		}

		mintExists, isConfirmed, existingTxID, err := s.db.CheckExistingMintForContract(
			singleton,
		)
		if err != nil {
			return fmt.Errorf(
				"failed to check existing mint for contract: %w",
				err,
			)
		}

		if mintExists {
			if isConfirmed {
				s.logger.Infof(
					"Existing mint transaction for contract %s is confirmed, ignoring new mempool transaction %s",
					singleton,
					tx.ID,
				)
				return nil
			} else {
				s.logger.Infow("Existing mint transaction for contract is unconfirmed, replacing with new mempool transaction",
					"contract_id", singleton,
					"existing_tx_id", existingTxID,
					"new_tx_id", tx.ID)
				if err := s.db.RollbackMintByTxID(existingTxID); err != nil {
					return fmt.Errorf("failed to rollback existing unconfirmed mint transaction: %w", err)
				}
			}
		}

		return s.writeMint(ctx, tx, regs, singleton)
	}

	if txExists {
		unconfirmed, err := s.db.CheckMintTxExistsAndUnconfirmed(tx.ID)
		if err != nil {
			return fmt.Errorf(
				"failed to check if mint tx is unconfirmed: %w",
				err,
			)
		}
		if unconfirmed {
			s.logger.Infow(
				"updating unconfirmed mint transaction to confirmed",
				"tx_hash",
				tx.ID,
			)
			if err := s.db.UpdateMintTxToConfirmed(tx.ID, ctx.BlockHeight, ctx.BlockHash, ctx.Timestamp); err != nil {
				return fmt.Errorf(
					"failed to update mint transaction to confirmed: %w",
					err,
				)
			}

			delete(s.mempoolTxMap, tx.ID)

			return nil
		}
		s.logger.Warnf(
			"[THIS SHOULD NOT HAPPEN] Mint transaction %s already exists and is confirmed, skipping",
			tx.ID,
		)
		return nil
	}

	return s.writeMint(ctx, tx, regs, singleton)
}

func (s *ChainEventProcessorActor) writeMint(
	ctx TxContext,
	tx *types.Transaction,
	regs *LokBoxRegisters,
	singleton string,
) error {
	keyId := regs.R5Bytes
	keyAmount := uint64(regs.R5Long)

	dbDesignates, err := extractDesignates(regs.R7)
	if err != nil {
		return fmt.Errorf("failed to extract designates: %w", err)
	}

	s.logger.Infow("Writing mint transaction to database",
		"tx_hash", tx.ID,
		"singleton", singleton,
		"confirmed", ctx.Confirmed)

	if ctx.Confirmed {
		return s.db.WriteCreateTokenLokAction(
			tx.ID,
			singleton,
			keyId,
			keyAmount,
			ctx.BlockHeight,
			ctx.BlockHash,
			ctx.Timestamp,
			dbDesignates,
		)
	}

	return s.db.WriteCreateTokenLokMempool(
		ctx.BlockHeight,
		ctx.BlockHash,
		singleton,
		tx.ID,
		keyId,
		keyAmount,
		dbDesignates,
	)
}

func (s *ChainEventProcessorActor) handleFunding(
	ctx TxContext,
	tx *types.Transaction,
	inputLokBox, lokBox ergo.Box,
	singleton string,
) error {
	txExists, err := s.db.CheckFundingTxExists(tx.ID)
	if err != nil {
		return fmt.Errorf("failed to check if funding tx exists: %w", err)
	}

	if !ctx.Confirmed {
		if txExists {
			s.logger.Infof(
				"mempool funding transaction %s already exists in database, skipping",
				tx.ID,
			)
			return nil
		}
		return s.writeFunding(ctx, tx, inputLokBox, lokBox, singleton)
	}

	if txExists {
		unconfirmed, err := s.db.CheckFundingTxExistsAndUnconfirmed(tx.ID)
		if err != nil {
			return fmt.Errorf(
				"failed to check if funding tx is unconfirmed: %w",
				err,
			)
		}
		if unconfirmed {
			s.logger.Infow(
				"updating unconfirmed funding transaction to confirmed",
				"tx_hash",
				tx.ID,
			)
			if err := s.db.UpdateFundingTxToConfirmed(tx.ID, ctx.BlockHeight, ctx.BlockHash, ctx.Timestamp); err != nil {
				return fmt.Errorf(
					"failed to update funding transaction to confirmed: %w",
					err,
				)
			}

			delete(s.mempoolTxMap, tx.ID)

			return nil
		}
		s.logger.Warnf(
			"[THIS SHOULD NOT HAPPEN] Funding transaction %s already exists and is confirmed, skipping",
			tx.ID,
		)
		return nil
	}

	return s.writeFunding(ctx, tx, inputLokBox, lokBox, singleton)
}

func (s *ChainEventProcessorActor) writeFunding(
	ctx TxContext,
	tx *types.Transaction,
	inputLokBox, lokBox ergo.Box,
	singleton string,
) error {
	inputErg := uint64(inputLokBox.BoxValue().Int64())
	erg := uint64(lokBox.BoxValue().Int64())
	ergDelta := erg - inputErg

	tokenEvents := makeTokenEvents(lokBox, &inputLokBox)

	s.logger.Infow("Writing funding transaction to database",
		"tx_hash", tx.ID,
		"singleton", singleton,
		"erg_delta", ergDelta,
		"confirmed", ctx.Confirmed)

	if ctx.Confirmed {
		return s.db.WriteFundingTx(
			singleton,
			tx.ID,
			ergDelta,
			ctx.BlockHeight,
			ctx.BlockHash,
			ctx.Timestamp,
			tokenEvents,
		)
	}

	return s.db.WriteFundingTxMempool(
		ctx.BlockHeight,
		singleton,
		tx.ID,
		ergDelta,
		tokenEvents,
	)
}

func (s *ChainEventProcessorActor) handleRedeem(
	ctx TxContext,
	tx *types.Transaction,
	inputLokBox ergo.Box,
	inputRegs *LokBoxRegisters,
	singleton string,
) error {
	txExists, err := s.db.CheckRedeemTxExists(tx.ID)
	if err != nil {
		return fmt.Errorf("failed to check if redeem tx exists: %w", err)
	}

	if !ctx.Confirmed {
		if txExists {
			s.logger.Infof(
				"Redeem transaction %s already exists in database, skipping",
				tx.ID,
			)
			return nil
		}

		redeemExists, isConfirmed, existingTxID, err := s.db.CheckExistingRedeemForContract(
			singleton,
		)
		if err != nil {
			return fmt.Errorf(
				"failed to check existing redeem for contract: %w",
				err,
			)
		}

		if redeemExists {
			if isConfirmed {
				s.logger.Infof(
					"Existing redeem transaction for contract %s is confirmed, ignoring new mempool transaction %s",
					singleton,
					tx.ID,
				)
				return nil
			} else {
				s.logger.Infow("Existing redeem transaction for contract is unconfirmed, replacing with new mempool transaction",
					"contract_id", singleton,
					"existing_tx_id", existingTxID,
					"new_tx_id", tx.ID)
				if err := s.db.RollbackRedeemByTxID(existingTxID); err != nil {
					return fmt.Errorf("failed to rollback existing unconfirmed redeem transaction: %w", err)
				}
			}
		}

		return s.writeRedeem(ctx, tx, inputRegs, singleton)
	}

	if txExists {
		unconfirmed, err := s.db.CheckRedeemTxExistsAndUnconfirmed(tx.ID)
		if err != nil {
			return fmt.Errorf(
				"failed to check if redeem tx is unconfirmed: %w",
				err,
			)
		}
		if unconfirmed {
			s.logger.Infow(
				"updating unconfirmed redeem transaction to confirmed",
				"tx_hash",
				tx.ID,
			)
			if err := s.db.UpdateRedeemTxToConfirmed(tx.ID, ctx.BlockHeight, ctx.BlockHash, ctx.Timestamp); err != nil {
				return fmt.Errorf(
					"failed to update redeem transaction to confirmed: %w",
					err,
				)
			}

			delete(s.mempoolTxMap, tx.ID)

			return nil
		}
		s.logger.Warnf(
			"[THIS SHOULD NOT HAPPEN] Redeem transaction %s already exists and is confirmed, skipping",
			tx.ID,
		)
		return nil
	}

	return s.writeRedeem(ctx, tx, inputRegs, singleton)
}

func (s *ChainEventProcessorActor) writeRedeem(
	ctx TxContext,
	tx *types.Transaction,
	inputRegs *LokBoxRegisters,
	singleton string,
) error {
	designatesErgoTreeBase16, err := extractDesignatesErgoTrees(inputRegs.R7)
	if err != nil {
		return fmt.Errorf("failed to extract designates ergo trees: %w", err)
	}

	blockHeight := ctx.BlockHeight
	if !ctx.Confirmed {
		blockHeight = ctx.TipHeight + 1
	}

	redeemType := matchRedeemLokAction(
		blockHeight,
		uint64(inputRegs.R6Long1),
		tx.Inputs,
		designatesErgoTreeBase16,
	)

	recipientAddress, err := getRecipientAddress(tx, 0)
	if err != nil {
		return fmt.Errorf("failed to get recipient address: %w", err)
	}

	s.logger.Infow("Writing redeem transaction to database",
		"tx_hash", tx.ID,
		"singleton", singleton,
		"redeem_type", redeemType,
		"confirmed", ctx.Confirmed)

	if ctx.Confirmed {
		return s.db.WriteRedeemTx(
			singleton,
			tx.ID,
			recipientAddress,
			redeemType,
			ctx.BlockHeight,
			ctx.BlockHash,
			ctx.Timestamp,
			true,
		)
	}

	return s.db.WriteRedeemTxMempool(
		ctx.BlockHeight,
		singleton,
		tx.ID,
		recipientAddress,
		redeemType,
	)
}

type ActionHandler func(*ChainEventProcessorActor, TxContext, *types.Transaction, ergo.Box, *LokBoxRegisters, string) error

type RedeemActionHandler func(*ChainEventProcessorActor, TxContext, *types.Transaction, ergo.Box, *LokBoxRegisters, string) error

var actionHandlers = map[int]ActionHandler{
	GenesisAction:        (*ChainEventProcessorActor).handleGenesis,
	CreateTokenLokAction: (*ChainEventProcessorActor).handleMint,
}

var redeemActionHandler RedeemActionHandler = (*ChainEventProcessorActor).handleRedeem

func (s *ChainEventProcessorActor) processLokTransaction(
	ctx TxContext,
	tx *types.Transaction,
	sigmaLokInputIndex int,
	sigmaLokOutputIndex int,
) error {
	action := GenesisAction

	if mempoolInfo, exists := s.mempoolTxMap[tx.ID]; exists && ctx.Confirmed {
		s.logger.Infof(
			"mempool tx found in cache, using action %d",
			mempoolInfo.Action,
		)
		action = mempoolInfo.Action
	} else if sigmaLokInputIndex > -1 {
		action = tryMatchLokBoxAction(ctx.BlockHeight, tx.Inputs[sigmaLokInputIndex], &s.nodeProvider)
	}

	if !ctx.Confirmed {
		s.addMempoolTx(tx.ID, action, ctx.TipHeight)
	}

	if sigmaLokOutputIndex > -1 {
		lokBox, regs, err := parseAndValidateLokBoxFromOutput(
			tx,
			sigmaLokOutputIndex,
		)
		if errors.Is(err, ErrNotLokBox) {
			s.logger.Infof("lok box not found in transaction %s", tx.ID)
			return nil
		}
		if err != nil {
			return fmt.Errorf("failed to parse and validate lok box: %w", err)
		}

		singleton, err := getSingleton(lokBox)
		if err != nil {
			return fmt.Errorf("failed to get singleton: %w", err)
		}

		if action == FundLokAction {
			inputLokBox, _, err := parseAndValidateLokBoxFromInput(
				tx,
				sigmaLokInputIndex,
			)
			if err != nil {
				return fmt.Errorf("failed to parse input lok box: %w", err)
			}
			return s.handleFunding(ctx, tx, inputLokBox, lokBox, singleton)
		}

		if handler, ok := actionHandlers[action]; ok {
			return handler(s, ctx, tx, lokBox, regs, singleton)
		}

		s.logger.Infof(
			"unknown action type %d in transaction %s",
			action,
			tx.ID,
		)
		return nil
	}

	if action == RedeemLokAction {
		inputLokBox, inputRegs, err := parseAndValidateLokBoxFromInput(
			tx,
			sigmaLokInputIndex,
		)
		if err != nil {
			return fmt.Errorf("failed to parse input lok box: %w", err)
		}

		singleton, err := getSingleton(inputLokBox)
		if err != nil {
			return fmt.Errorf("failed to get singleton: %w", err)
		}

		return redeemActionHandler(
			s,
			ctx,
			tx,
			inputLokBox,
			inputRegs,
			singleton,
		)
	}

	return nil
}
