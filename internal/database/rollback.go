package database

import (
	"fmt"

	"gorm.io/gorm"
)

func (d *Database) Rollback(targetHeight uint64) error {
	// Delete all blocks with height >= target_height
	// CASCADE will automatically delete all related records
	result := d.db.Exec("DELETE FROM block WHERE height >= ?", targetHeight)

	if result.Error != nil {
		return fmt.Errorf(
			"failed to rollback to height %d (inclusive): %w",
			targetHeight,
			result.Error,
		)
	}

	// Log how many blocks were affected
	if result.RowsAffected > 0 {
		d.logger.Infow("rollback completed",
			"height", targetHeight,
			"deleted_blocks_count", result.RowsAffected,
		)
	} else {
		d.logger.Infow("rollback completed",
			"height", targetHeight,
			"deleted_blocks_count", result.RowsAffected,
		)
	}

	return nil
}

// RollbackGenesisByTxID deletes a contract and all related records by genesis transaction ID
// This is used when mempool transactions are reverted
func (d *Database) RollbackGenesisByTxID(genesisTxID string) error {
	return d.db.Transaction(func(tx *gorm.DB) error {
		// First, find the contract to get its singleton for logging
		var contract Contract
		err := tx.Where("genesis_tx = ?", genesisTxID).First(&contract).Error
		if err != nil {
			if err == gorm.ErrRecordNotFound {
				d.logger.Infow("rollback genesis: contract not found",
					"genesis_tx", genesisTxID,
				)
				return nil // Not an error if contract doesn't exist
			}
			return fmt.Errorf(
				"failed to find contract with genesis_tx %s: %w",
				genesisTxID,
				err,
			)
		}

		// Delete the contract - CASCADE will automatically delete all related records:
		// - Key records (via contract_id -> contract.singleton)
		// - TokenEvent records (via contract_id -> contract.singleton)
		// - Designate records (via contract_id -> contract.singleton)
		// - FundingTx records (via contract_id -> contract.singleton)
		// - MintTx records (via contract_id -> contract.singleton)
		// - RedeemTx records (via contract_id -> contract.singleton)
		result := tx.Where("genesis_tx = ?", genesisTxID).Delete(&Contract{})

		if result.Error != nil {
			return fmt.Errorf(
				"failed to delete contract with genesis_tx %s: %w",
				genesisTxID,
				result.Error,
			)
		}

		if result.RowsAffected > 0 {
			d.logger.Infow("rollback genesis completed",
				"genesis_tx", genesisTxID,
				"singleton", contract.Singleton,
				"deleted_contracts_count", result.RowsAffected,
			)
		} else {
			d.logger.Infow("rollback genesis: no contracts deleted",
				"genesis_tx", genesisTxID,
			)
		}

		return nil
	})
}

// RollbackMintByTxID deletes a mint transaction and related key/designate records
// This is used when mempool mint transactions are reverted
func (d *Database) RollbackMintByTxID(mintTxID string) error {
	return d.db.Transaction(func(tx *gorm.DB) error {
		// First, find the mint transaction to get its contract_id
		var mintTx MintTx
		err := tx.Where("tx = ?", mintTxID).First(&mintTx).Error
		if err != nil {
			if err == gorm.ErrRecordNotFound {
				d.logger.Infow("rollback mint: mint transaction not found",
					"mint_tx", mintTxID,
				)
				return nil // Not an error if mint transaction doesn't exist
			}
			return fmt.Errorf(
				"failed to find mint transaction %s: %w",
				mintTxID,
				err,
			)
		}

		contractID := mintTx.ContractID

		// Delete the mint transaction record
		result := tx.Where("tx = ?", mintTxID).Delete(&MintTx{})
		if result.Error != nil {
			return fmt.Errorf(
				"failed to delete mint transaction %s: %w",
				mintTxID,
				result.Error,
			)
		}
		mintTxDeleted := result.RowsAffected

		// Delete key records for this contract
		result = tx.Where("contract_id = ?", contractID).Delete(&Key{})
		if result.Error != nil {
			return fmt.Errorf(
				"failed to delete key records for contract %s: %w",
				contractID,
				result.Error,
			)
		}
		keysDeleted := result.RowsAffected

		// Delete designate records for this contract
		result = tx.Where("contract_id = ?", contractID).Delete(&Designate{})
		if result.Error != nil {
			return fmt.Errorf(
				"failed to delete designate records for contract %s: %w",
				contractID,
				result.Error,
			)
		}
		designatesDeleted := result.RowsAffected

		d.logger.Infow("rollback mint completed",
			"mint_tx", mintTxID,
			"contract_id", contractID,
			"deleted_mint_txs", mintTxDeleted,
			"deleted_keys", keysDeleted,
			"deleted_designates", designatesDeleted,
		)

		return nil
	})
}

// RollbackFundingByTxID deletes a funding transaction and its associated token events
// This is used when mempool funding transactions are reverted
func (d *Database) RollbackFundingByTxID(fundingTxID string) error {
	return d.db.Transaction(func(tx *gorm.DB) error {
		// First, find the funding transaction to get its contract_id
		var fundingTx FundingTx
		err := tx.Where("tx = ?", fundingTxID).First(&fundingTx).Error
		if err != nil {
			if err == gorm.ErrRecordNotFound {
				d.logger.Infow(
					"rollback funding: funding transaction not found",
					"funding_tx",
					fundingTxID,
				)
				return nil // Not an error if funding transaction doesn't exist
			}
			return fmt.Errorf(
				"failed to find funding transaction %s: %w",
				fundingTxID,
				err,
			)
		}

		contractID := fundingTx.ContractID

		// Delete the funding transaction record
		result := tx.Where("tx = ?", fundingTxID).Delete(&FundingTx{})
		if result.Error != nil {
			return fmt.Errorf(
				"failed to delete funding transaction %s: %w",
				fundingTxID,
				result.Error,
			)
		}
		fundingTxDeleted := result.RowsAffected

		// Delete token events for this transaction
		result = tx.Where("tx = ?", fundingTxID).Delete(&TokenEvent{})
		if result.Error != nil {
			return fmt.Errorf(
				"failed to delete token events for transaction %s: %w",
				fundingTxID,
				result.Error,
			)
		}
		tokenEventsDeleted := result.RowsAffected

		d.logger.Infow("rollback funding completed",
			"funding_tx", fundingTxID,
			"contract_id", contractID,
			"deleted_funding_txs", fundingTxDeleted,
			"deleted_token_events", tokenEventsDeleted,
		)

		return nil
	})
}

// RollbackRedeemByTxID deletes a redeem transaction
// This is used when mempool redeem transactions are reverted
func (d *Database) RollbackRedeemByTxID(redeemTxID string) error {
	return d.db.Transaction(func(tx *gorm.DB) error {
		// First, find the redeem transaction to get its contract_id for logging
		var redeemTx RedeemTx
		err := tx.Where("tx = ?", redeemTxID).First(&redeemTx).Error
		if err != nil {
			if err == gorm.ErrRecordNotFound {
				d.logger.Infow("rollback redeem: redeem transaction not found",
					"redeem_tx", redeemTxID,
				)
				return nil // Not an error if redeem transaction doesn't exist
			}
			return fmt.Errorf(
				"failed to find redeem transaction %s: %w",
				redeemTxID,
				err,
			)
		}

		contractID := redeemTx.ContractID

		// Delete the redeem transaction record
		result := tx.Where("tx = ?", redeemTxID).Delete(&RedeemTx{})
		if result.Error != nil {
			return fmt.Errorf(
				"failed to delete redeem transaction %s: %w",
				redeemTxID,
				result.Error,
			)
		}
		redeemTxDeleted := result.RowsAffected

		d.logger.Infow("rollback redeem completed",
			"redeem_tx", redeemTxID,
			"contract_id", contractID,
			"deleted_redeem_txs", redeemTxDeleted,
		)

		return nil
	})
}
