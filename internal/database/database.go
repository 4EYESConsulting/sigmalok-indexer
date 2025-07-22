package database

import (
	"4EYESConsulting/sigmalok-indexer/internal/config"
	"4EYESConsulting/sigmalok-indexer/internal/logging"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"go.uber.org/zap"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

type Database struct {
	db     *gorm.DB
	logger *zap.SugaredLogger
}

func New(cfg *config.Config) (*Database, error) {
	dataDir := cfg.Storage.Directory
	// Make sure that we can read data dir, and create if it doesn't exist
	if _, err := os.Stat(dataDir); err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("failed to read data dir: %w", err)
		}
		// Create data directory
		if err := os.MkdirAll(dataDir, fs.ModePerm); err != nil {
			return nil, fmt.Errorf("failed to create data dir: %w", err)
		}
	}
	// Open sqlite DB
	dbPath := filepath.Join(
		dataDir,
		"data.sqlite",
	)
	// WAL journal mode and synchronous NORMAL for better performance
	connOpts := "_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"
	db, err := gorm.Open(
		sqlite.Open(
			fmt.Sprintf("file:%s?%s", dbPath, connOpts),
		),
		&gorm.Config{
			Logger: gormlogger.Discard,
		},
	)
	if err != nil {
		return nil, err
	}
	d := &Database{
		db:     db,
		logger: logging.GetLogger().With("actor", "Database"),
	}
	for _, model := range MigrateModels {
		if err := d.db.AutoMigrate(model); err != nil {
			return nil, err
		}
	}
	if err := d.createIndices(); err != nil {
		return nil, fmt.Errorf("failed to create indices: %w", err)
	}
	return d, nil
}

func (d *Database) createIndices() error {
	return nil
}

// ensureBlockExists creates a block record if it doesn't already exist
func (d *Database) ensureBlockExists(
	tx *gorm.DB,
	height uint64,
	hash string,
	blockTimestamp time.Time,
) error {
	var block Block
	err := tx.Where("height = ?", int(height)).First(&block).Error
	if err == nil {
		// Block already exists
		return nil
	}
	if err != gorm.ErrRecordNotFound {
		return fmt.Errorf("failed to check if block exists: %w", err)
	}

	newBlock := Block{
		Height:    int(height),
		Hash:      hash,
		Timestamp: blockTimestamp.Unix(),
	}
	if err := tx.Create(&newBlock).Error; err != nil {
		return fmt.Errorf("failed to create block: %w", err)
	}
	return nil
}

// WriteGenesisTx creates a new contract with the genesis transaction information
func (d *Database) WriteGenesisTx(
	singleton string,
	blockHeight uint64,
	blockHash string,
	blockTimestamp time.Time,
	name *string,
	creator string,
	benefactor string,
	genesisErg uint64,
	deadline uint64,
	oracleNFT *string,
	oracleValue *uint64,
	oracleIsGreaterThan *bool,
	genesisTx string,
	feeHash string,
	feeAmount int,
	genesisConfirmed bool,
	tokenEvents []TokenEvent,
) error {
	return d.db.Transaction(func(tx *gorm.DB) error {
		if err := d.ensureBlockExists(tx, blockHeight, blockHash, blockTimestamp); err != nil {
			return err
		}

		creatorAddr := Address{Address: creator}
		if err := tx.FirstOrCreate(&creatorAddr, Address{Address: creator}).Error; err != nil {
			return fmt.Errorf("failed to create creator address: %w", err)
		}

		benefactorAddr := Address{Address: benefactor}
		if err := tx.FirstOrCreate(&benefactorAddr, Address{Address: benefactor}).Error; err != nil {
			return fmt.Errorf("failed to create benefactor address: %w", err)
		}

		var feeParam FeeParameter
		if err := tx.Where("fee_hash = ? AND fee_amount = ?", feeHash, feeAmount).
			FirstOrCreate(&feeParam, FeeParameter{
				ID:        uuid.New(),
				FeeHash:   feeHash,
				FeeAmount: feeAmount,
			}).Error; err != nil {
			return fmt.Errorf("failed to find or create fee parameter: %w", err)
		}

		contract := Contract{
			Singleton:           singleton,
			BlockHeight:         blockHeight,
			BlockHash:           blockHash,
			Name:                name,
			Creator:             creator,
			Benefactor:          benefactor,
			GenesisErg:          genesisErg,
			Deadline:            deadline,
			OracleNFT:           oracleNFT,
			OracleValue:         oracleValue,
			OracleIsGreaterThan: oracleIsGreaterThan,
			GenesisTx:           genesisTx,
			ParameterID:         feeParam.ID,
			GenesisConfirmed:    genesisConfirmed,
		}

		if err := tx.Create(&contract).Error; err != nil {
			return fmt.Errorf("failed to create contract: %w", err)
		}

		for _, tokenEvent := range tokenEvents {
			tokenEvent.ContractID = singleton
			tokenEvent.Tx = genesisTx
			tokenEvent.BlockHeight = blockHeight
			if err := tx.Create(&tokenEvent).Error; err != nil {
				return fmt.Errorf("failed to create token event: %w", err)
			}
		}

		return nil
	})
}

// WriteCreateTokenLokAction creates a new mint transaction record
func (d *Database) WriteCreateTokenLokAction(
	txId string,
	contractID string,
	keyID string,
	keyAmount uint64,
	blockHeight uint64,
	blockHash string,
	blockTimestamp time.Time,
	designates []Designate,
) error {
	d.logger.Infow("attempting to write confirmed mint action",
		"contract_id", contractID,
		"key_id", keyID,
		"key_amount", keyAmount,
		"block_height", blockHeight)

	return d.db.Transaction(func(tx *gorm.DB) error {
		var contract Contract
		if err := tx.Where("singleton = ?", contractID).First(&contract).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				d.logger.Errorw("contract not found for confirmed mint action",
					"contract_id", contractID)
				return fmt.Errorf(
					"contract with singleton %s not found",
					contractID,
				)
			}
			return fmt.Errorf("failed to find contract: %w", err)
		}

		mintTx := MintTx{
			ContractID:  contractID,
			Tx:          txId,
			BlockHeight: blockHeight,
			Confirmed:   true,
		}

		if err := tx.Create(&mintTx).Error; err != nil {
			d.logger.Errorw("failed to create confirmed mint tx",
				"contract_id", contractID,
				"key_id", keyID,
				"error", err)
			return fmt.Errorf("failed to create mint transaction: %w", err)
		}

		key := Key{
			ContractID:  contractID,
			ID:          keyID,
			Amount:      keyAmount,
			BlockHeight: blockHeight,
		}

		if err := tx.Create(&key).Error; err != nil {
			d.logger.Errorw("failed to create key",
				"contract_id", contractID,
				"key_id", keyID,
				"error", err)
			return fmt.Errorf("failed to create key: %w", err)
		}

		for _, designate := range designates {
			designate.ContractID = contractID
			designate.BlockHeight = blockHeight
			if err := tx.Create(&designate).Error; err != nil {
				d.logger.Errorw("failed to create designate",
					"contract_id", contractID,
					"address", designate.Address,
					"error", err)
				return fmt.Errorf("failed to create designate: %w", err)
			}
		}

		d.logger.Infow("successfully wrote confirmed mint action",
			"contract_id", contractID,
			"key_id", keyID,
			"block_height", blockHeight,
			"designates_count", len(designates))

		return nil
	})
}

// WriteFundingTx atomically creates a funding transaction and all associated token events
func (d *Database) WriteFundingTx(
	singleton string,
	tx string,
	ergDelta uint64,
	blockHeight uint64,
	blockHash string,
	blockTimestamp time.Time,
	tokenEvents []TokenEvent,
) error {
	return d.db.Transaction(func(txDB *gorm.DB) error {
		var contract Contract
		if err := txDB.Where("singleton = ?", singleton).First(&contract).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return fmt.Errorf(
					"contract with singleton %s not found",
					singleton,
				)
			}
			return fmt.Errorf("failed to find contract: %w", err)
		}

		if err := d.ensureBlockExists(txDB, blockHeight, blockHash, blockTimestamp); err != nil {
			return err
		}

		fundingTx := FundingTx{
			ContractID:  singleton,
			Tx:          tx,
			ErgDelta:    ergDelta,
			BlockHeight: blockHeight,
			WrittenAt:   time.Now().Unix(),
			Confirmed:   true,
		}

		if err := txDB.Create(&fundingTx).Error; err != nil {
			return fmt.Errorf("failed to create funding transaction: %w", err)
		}

		for _, tokenEvent := range tokenEvents {
			tokenEvent.ContractID = singleton
			tokenEvent.Tx = tx
			tokenEvent.BlockHeight = blockHeight
			if err := txDB.Create(&tokenEvent).Error; err != nil {
				return fmt.Errorf(
					"failed to create token event for asset %s: %w",
					tokenEvent.AssetID,
					err,
				)
			}
		}

		return nil
	})
}

// WriteRedeemTx creates a new redeem transaction record
func (d *Database) WriteRedeemTx(
	contractID string,
	tx string,
	recipientAddress string,
	redeemType RedeemType,
	blockHeight uint64,
	blockHash string,
	blockTimestamp time.Time,
	confirmed bool,
) error {
	return d.db.Transaction(func(txDB *gorm.DB) error {
		var contract Contract
		if err := txDB.Where("singleton = ?", contractID).First(&contract).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return fmt.Errorf(
					"contract with singleton %s not found",
					contractID,
				)
			}
			return fmt.Errorf("failed to find contract: %w", err)
		}

		if err := d.ensureBlockExists(txDB, blockHeight, blockHash, blockTimestamp); err != nil {
			return err
		}

		recipientAddr := Address{Address: recipientAddress}
		if err := txDB.FirstOrCreate(&recipientAddr, Address{Address: recipientAddress}).Error; err != nil {
			return fmt.Errorf("failed to create recipient address: %w", err)
		}

		redeemTx := RedeemTx{
			ContractID:       contractID,
			Tx:               tx,
			RecipientAddress: recipientAddress,
			Type:             redeemType,
			BlockHeight:      blockHeight,
			Confirmed:        confirmed,
			WrittenAt:        time.Now().Unix(),
		}

		if err := txDB.Create(&redeemTx).Error; err != nil {
			return fmt.Errorf("failed to create redeem transaction: %w", err)
		}

		return nil
	})
}

// CheckGenesisTxExists checks if a genesis transaction already exists in the contract table
func (d *Database) CheckGenesisTxExists(genesisTx string) (bool, error) {
	var count int64
	err := d.db.Model(&Contract{}).
		Where("genesis_tx = ?", genesisTx).
		Count(&count).
		Error
	if err != nil {
		return false, fmt.Errorf(
			"failed to check if genesis tx exists: %w",
			err,
		)
	}
	return count > 0, nil
}

// CheckSingletonExists checks if a singleton already exists and returns its confirmation status
func (d *Database) CheckSingletonExists(
	singleton string,
) (exists bool, confirmed bool, err error) {
	var contract Contract
	err = d.db.Where("singleton = ?", singleton).First(&contract).Error
	if err == gorm.ErrRecordNotFound {
		return false, false, nil
	}
	if err != nil {
		return false, false, fmt.Errorf(
			"failed to check if singleton exists: %w",
			err,
		)
	}
	return true, contract.GenesisConfirmed, nil
}

// WriteGenesisTxMempool creates a new contract with the genesis transaction information from mempool (unconfirmed)
func (d *Database) WriteGenesisTxMempool(
	blockHeight uint64,
	blockHash string,
	singleton string,
	name *string,
	creator string,
	benefactor string,
	genesisErg uint64,
	deadline uint64,
	oracleNFT *string,
	oracleValue *uint64,
	oracleIsGreaterThan *bool,
	genesisTx string,
	feeHash string,
	feeAmount int,
	tokenEvents []TokenEvent,
) error {
	return d.db.Transaction(func(tx *gorm.DB) error {
		creatorAddr := Address{Address: creator}
		if err := tx.FirstOrCreate(&creatorAddr, Address{Address: creator}).Error; err != nil {
			return fmt.Errorf("failed to create creator address: %w", err)
		}

		benefactorAddr := Address{Address: benefactor}
		if err := tx.FirstOrCreate(&benefactorAddr, Address{Address: benefactor}).Error; err != nil {
			return fmt.Errorf("failed to create benefactor address: %w", err)
		}

		var feeParam FeeParameter
		if err := tx.Where("fee_hash = ? AND fee_amount = ?", feeHash, feeAmount).
			FirstOrCreate(&feeParam, FeeParameter{
				ID:        uuid.New(),
				FeeHash:   feeHash,
				FeeAmount: feeAmount,
			}).Error; err != nil {
			return fmt.Errorf("failed to find or create fee parameter: %w", err)
		}

		contract := Contract{
			Singleton:           singleton,
			BlockHeight:         blockHeight,
			BlockHash:           blockHash,
			Name:                name,
			Creator:             creator,
			Benefactor:          benefactor,
			GenesisErg:          genesisErg,
			Deadline:            deadline,
			OracleNFT:           oracleNFT,
			OracleValue:         oracleValue,
			OracleIsGreaterThan: oracleIsGreaterThan,
			GenesisTx:           genesisTx,
			ParameterID:         feeParam.ID,
			GenesisConfirmed:    false,
		}

		if err := tx.Create(&contract).Error; err != nil {
			return fmt.Errorf("failed to create contract: %w", err)
		}

		for _, tokenEvent := range tokenEvents {
			tokenEvent.ContractID = singleton
			tokenEvent.Tx = genesisTx
			tokenEvent.BlockHeight = blockHeight
			if err := tx.Create(&tokenEvent).Error; err != nil {
				return fmt.Errorf("failed to create token event: %w", err)
			}
		}

		return nil
	})
}

// CheckMintTxExists checks if a mint transaction already exists in the mint_tx table
func (d *Database) CheckMintTxExists(mintTx string) (bool, error) {
	var count int64
	err := d.db.Model(&MintTx{}).Where("tx = ?", mintTx).Count(&count).Error
	if err != nil {
		return false, fmt.Errorf("failed to check if mint tx exists: %w", err)
	}
	return count > 0, nil
}

// CheckExistingMintForContract checks if there's an existing mint transaction for a contract/singleton
func (d *Database) CheckExistingMintForContract(
	contractID string,
) (exists bool, confirmed bool, existingTxID string, err error) {
	var mint MintTx
	err = d.db.Where("contract_id = ?", contractID).First(&mint).Error
	if err == gorm.ErrRecordNotFound {
		return false, false, "", nil
	}
	if err != nil {
		return false, false, "", fmt.Errorf(
			"failed to check existing mint for contract: %w",
			err,
		)
	}
	return true, mint.Confirmed, mint.Tx, nil
}

// WriteCreateTokenLokMempool creates key and designate records for mempool mint transactions (unconfirmed)
func (d *Database) WriteCreateTokenLokMempool(
	blockHeight uint64,
	blockHash string,
	contractID string,
	mintTx string,
	keyID string,
	keyAmount uint64,
	designates []Designate,
) error {
	d.logger.Infow("attempting to write mempool mint tx",
		"contract_id", contractID,
		"tx", mintTx,
		"block_height", blockHeight)

	return d.db.Transaction(func(tx *gorm.DB) error {
		var contract Contract
		if err := tx.Where("singleton = ?", contractID).First(&contract).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				d.logger.Errorw("contract not found for mempool mint tx",
					"contract_id", contractID,
					"tx", mintTx)
				return fmt.Errorf(
					"contract with singleton %s not found",
					contractID,
				)
			}
			return fmt.Errorf("failed to find contract: %w", err)
		}

		mintTxRecord := MintTx{
			ContractID:  contractID,
			Tx:          mintTx,
			BlockHeight: blockHeight,
			Confirmed:   false,
			WrittenAt:   time.Now().Unix(),
		}

		if err := tx.Create(&mintTxRecord).Error; err != nil {
			d.logger.Errorw("failed to create mempool mint tx",
				"contract_id", contractID,
				"tx", mintTx,
				"error", err)
			return fmt.Errorf("failed to create mint transaction: %w", err)
		}

		d.logger.Infow("successfully wrote mempool mint tx",
			"contract_id", contractID,
			"tx", mintTx,
			"block_height", blockHeight)

		key := Key{
			ContractID:  contractID,
			ID:          keyID,
			Amount:      keyAmount,
			BlockHeight: blockHeight,
		}
		if err := tx.Create(&key).Error; err != nil {
			return fmt.Errorf("failed to create key: %w", err)
		}

		for _, designate := range designates {
			designateAddr := Address{Address: designate.Address}
			if err := tx.FirstOrCreate(&designateAddr, Address{Address: designate.Address}).Error; err != nil {
				return fmt.Errorf("failed to create designate address: %w", err)
			}

			designate.ContractID = contractID
			designate.BlockHeight = blockHeight
			if err := tx.Create(&designate).Error; err != nil {
				return fmt.Errorf("failed to create designate: %w", err)
			}
		}

		return nil
	})
}

// CheckFundingTxExists checks if a funding transaction already exists in the funding_tx table
func (d *Database) CheckFundingTxExists(fundingTx string) (bool, error) {
	var count int64
	err := d.db.Model(&FundingTx{}).
		Where("tx = ?", fundingTx).
		Count(&count).
		Error
	if err != nil {
		return false, fmt.Errorf(
			"failed to check if funding tx exists: %w",
			err,
		)
	}
	return count > 0, nil
}

// WriteFundingTxMempool creates a funding transaction and token events for mempool (unconfirmed)
func (d *Database) WriteFundingTxMempool(
	blockHeight uint64,
	singleton string,
	tx string,
	ergDelta uint64,
	tokenEvents []TokenEvent,
) error {
	return d.db.Transaction(func(txDB *gorm.DB) error {
		var contract Contract
		if err := txDB.Where("singleton = ?", singleton).First(&contract).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return fmt.Errorf(
					"contract with singleton %s not found",
					singleton,
				)
			}
			return fmt.Errorf("failed to find contract: %w", err)
		}

		fundingTx := FundingTx{
			ContractID:  singleton,
			Tx:          tx,
			ErgDelta:    ergDelta,
			BlockHeight: blockHeight,
			WrittenAt:   time.Now().Unix(),
			Confirmed:   false,
		}

		if err := txDB.Create(&fundingTx).Error; err != nil {
			return fmt.Errorf("failed to create funding transaction: %w", err)
		}

		for _, tokenEvent := range tokenEvents {
			tokenEvent.ContractID = singleton
			tokenEvent.Tx = tx
			tokenEvent.BlockHeight = blockHeight
			if err := txDB.Create(&tokenEvent).Error; err != nil {
				return fmt.Errorf(
					"failed to create token event for asset %s: %w",
					tokenEvent.AssetID,
					err,
				)
			}
		}

		return nil
	})
}

// CheckRedeemTxExists checks if a redeem transaction already exists in the redeem_tx table
func (d *Database) CheckRedeemTxExists(redeemTx string) (bool, error) {
	var count int64
	err := d.db.Model(&RedeemTx{}).Where("tx = ?", redeemTx).Count(&count).Error
	if err != nil {
		return false, fmt.Errorf("failed to check if redeem tx exists: %w", err)
	}
	return count > 0, nil
}

// CheckExistingRedeemForContract checks if there's an existing redeem transaction for a contract/singleton
func (d *Database) CheckExistingRedeemForContract(
	contractID string,
) (exists bool, confirmed bool, existingTxID string, err error) {
	var redeem RedeemTx
	err = d.db.Where("contract_id = ?", contractID).First(&redeem).Error
	if err == gorm.ErrRecordNotFound {
		return false, false, "", nil
	}
	if err != nil {
		return false, false, "", fmt.Errorf(
			"failed to check existing redeem for contract: %w",
			err,
		)
	}
	return true, redeem.Confirmed, redeem.Tx, nil
}

// WriteRedeemTxMempool creates a redeem transaction record for mempool (unconfirmed)
func (d *Database) WriteRedeemTxMempool(
	blockHeight uint64,
	contractID string,
	tx string,
	recipientAddress string,
	redeemType RedeemType,
) error {
	return d.db.Transaction(func(txDB *gorm.DB) error {
		var contract Contract
		if err := txDB.Where("singleton = ?", contractID).First(&contract).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return fmt.Errorf(
					"contract with singleton %s not found",
					contractID,
				)
			}
			return fmt.Errorf("failed to find contract: %w", err)
		}

		recipientAddr := Address{Address: recipientAddress}
		if err := txDB.FirstOrCreate(&recipientAddr, Address{Address: recipientAddress}).Error; err != nil {
			return fmt.Errorf("failed to create recipient address: %w", err)
		}

		redeemTx := RedeemTx{
			ContractID:       contractID,
			Tx:               tx,
			RecipientAddress: recipientAddress,
			Type:             redeemType,
			BlockHeight:      blockHeight,
			Confirmed:        false,
			WrittenAt:        time.Now().Unix(),
		}

		if err := txDB.Create(&redeemTx).Error; err != nil {
			return fmt.Errorf("failed to create redeem transaction: %w", err)
		}

		return nil
	})
}

// UpdateGenesisTxToConfirmed updates an existing unconfirmed genesis transaction with correct block information and sets it to confirmed
func (d *Database) UpdateGenesisTxToConfirmed(
	genesisTx string,
	blockHeight uint64,
	blockHash string,
	blockTimestamp time.Time,
) error {
	return d.db.Transaction(func(tx *gorm.DB) error {
		if err := d.ensureBlockExists(tx, blockHeight, blockHash, blockTimestamp); err != nil {
			return err
		}

		result := tx.Model(&Contract{}).
			Where("genesis_tx = ? AND genesis_confirmed = ?", genesisTx, false).
			Updates(map[string]interface{}{
				"block_height":      blockHeight,
				"block_hash":        blockHash,
				"genesis_confirmed": true,
			})

		if result.Error != nil {
			return fmt.Errorf(
				"failed to update genesis transaction: %w",
				result.Error,
			)
		}

		if result.RowsAffected == 0 {
			return fmt.Errorf(
				"no unconfirmed genesis transaction found with tx %s",
				genesisTx,
			)
		}

		if err := tx.Model(&TokenEvent{}).
			Where("tx = ?", genesisTx).
			Update("block_height", blockHeight).Error; err != nil {
			return fmt.Errorf("failed to update token events: %w", err)
		}

		d.logger.Infow("updated genesis transaction to confirmed",
			"genesis_tx", genesisTx,
			"block_height", blockHeight,
			"block_hash", blockHash)

		return nil
	})
}

// UpdateMintTxToConfirmed updates an existing unconfirmed mint transaction with correct block information and sets it to confirmed
func (d *Database) UpdateMintTxToConfirmed(
	mintTx string,
	blockHeight uint64,
	blockHash string,
	blockTimestamp time.Time,
) error {
	return d.db.Transaction(func(tx *gorm.DB) error {
		if err := d.ensureBlockExists(tx, blockHeight, blockHash, blockTimestamp); err != nil {
			return err
		}

		result := tx.Model(&MintTx{}).
			Where("tx = ? AND confirmed = ?", mintTx, false).
			Updates(map[string]interface{}{
				"block_height": blockHeight,
				"confirmed":    true,
			})

		if result.Error != nil {
			return fmt.Errorf(
				"failed to update mint transaction: %w",
				result.Error,
			)
		}

		if result.RowsAffected == 0 {
			return fmt.Errorf(
				"no unconfirmed mint transaction found with tx %s",
				mintTx,
			)
		}

		if err := tx.Model(&Key{}).
			Where("contract_id = (SELECT contract_id FROM mint_tx WHERE tx = ?)", mintTx).
			Update("block_height", blockHeight).Error; err != nil {
			return fmt.Errorf("failed to update key: %w", err)
		}

		if err := tx.Model(&Designate{}).
			Where("contract_id = (SELECT contract_id FROM mint_tx WHERE tx = ?)", mintTx).
			Update("block_height", blockHeight).Error; err != nil {
			return fmt.Errorf("failed to update designates: %w", err)
		}

		d.logger.Infow("updated mint transaction to confirmed",
			"mint_tx", mintTx,
			"block_height", blockHeight,
			"block_hash", blockHash)

		return nil
	})
}

// UpdateFundingTxToConfirmed updates an existing unconfirmed funding transaction with correct block information and sets it to confirmed
func (d *Database) UpdateFundingTxToConfirmed(
	fundingTx string,
	blockHeight uint64,
	blockHash string,
	blockTimestamp time.Time,
) error {
	return d.db.Transaction(func(tx *gorm.DB) error {
		if err := d.ensureBlockExists(tx, blockHeight, blockHash, blockTimestamp); err != nil {
			return err
		}

		result := tx.Model(&FundingTx{}).
			Where("tx = ? AND confirmed = ?", fundingTx, false).
			Updates(map[string]interface{}{
				"block_height": blockHeight,
				"confirmed":    true,
			})

		if result.Error != nil {
			return fmt.Errorf(
				"failed to update funding transaction: %w",
				result.Error,
			)
		}

		if result.RowsAffected == 0 {
			return fmt.Errorf(
				"no unconfirmed funding transaction found with tx %s",
				fundingTx,
			)
		}

		if err := tx.Model(&TokenEvent{}).
			Where("tx = ?", fundingTx).
			Update("block_height", blockHeight).Error; err != nil {
			return fmt.Errorf("failed to update token events: %w", err)
		}

		d.logger.Infow("updated funding transaction to confirmed",
			"funding_tx", fundingTx,
			"block_height", blockHeight,
			"block_hash", blockHash)

		return nil
	})
}

// UpdateRedeemTxToConfirmed updates an existing unconfirmed redeem transaction with correct block information and sets it to confirmed
func (d *Database) UpdateRedeemTxToConfirmed(
	redeemTx string,
	blockHeight uint64,
	blockHash string,
	blockTimestamp time.Time,
) error {
	return d.db.Transaction(func(tx *gorm.DB) error {
		if err := d.ensureBlockExists(tx, blockHeight, blockHash, blockTimestamp); err != nil {
			return err
		}

		result := tx.Model(&RedeemTx{}).
			Where("tx = ? AND confirmed = ?", redeemTx, false).
			Updates(map[string]interface{}{
				"block_height": blockHeight,
				"confirmed":    true,
			})

		if result.Error != nil {
			return fmt.Errorf(
				"failed to update redeem transaction: %w",
				result.Error,
			)
		}

		if result.RowsAffected == 0 {
			return fmt.Errorf(
				"no unconfirmed redeem transaction found with tx %s",
				redeemTx,
			)
		}

		d.logger.Infow("updated redeem transaction to confirmed",
			"redeem_tx", redeemTx,
			"block_height", blockHeight,
			"block_hash", blockHash)

		return nil
	})
}

// CheckGenesisTxExistsAndUnconfirmed checks if a genesis transaction exists and is unconfirmed
func (d *Database) CheckGenesisTxExistsAndUnconfirmed(
	genesisTx string,
) (bool, error) {
	var count int64
	err := d.db.Model(&Contract{}).
		Where("genesis_tx = ? AND genesis_confirmed = ?", genesisTx, false).
		Count(&count).
		Error
	if err != nil {
		return false, fmt.Errorf(
			"failed to check if unconfirmed genesis tx exists: %w",
			err,
		)
	}
	return count > 0, nil
}

// CheckMintTxExistsAndUnconfirmed checks if a mint transaction exists and is unconfirmed
func (d *Database) CheckMintTxExistsAndUnconfirmed(
	mintTx string,
) (bool, error) {
	var count int64
	err := d.db.Model(&MintTx{}).
		Where("tx = ? AND confirmed = ?", mintTx, false).
		Count(&count).
		Error
	if err != nil {
		return false, fmt.Errorf(
			"failed to check if unconfirmed mint tx exists: %w",
			err,
		)
	}
	return count > 0, nil
}

// CheckFundingTxExistsAndUnconfirmed checks if a funding transaction exists and is unconfirmed
func (d *Database) CheckFundingTxExistsAndUnconfirmed(
	fundingTx string,
) (bool, error) {
	var count int64
	err := d.db.Model(&FundingTx{}).
		Where("tx = ?", fundingTx).
		Count(&count).
		Error
	if err != nil {
		return false, fmt.Errorf(
			"failed to check if funding tx exists: %w",
			err,
		)
	}
	return count > 0, nil
}

// CheckRedeemTxExistsAndUnconfirmed checks if a redeem transaction exists and is unconfirmed
func (d *Database) CheckRedeemTxExistsAndUnconfirmed(
	redeemTx string,
) (bool, error) {
	var count int64
	err := d.db.Model(&RedeemTx{}).
		Where("tx = ? AND confirmed = ?", redeemTx, false).
		Count(&count).
		Error
	if err != nil {
		return false, fmt.Errorf(
			"failed to check if unconfirmed redeem tx exists: %w",
			err,
		)
	}
	return count > 0, nil
}

// GetGenesisTransactionBySingleton returns the genesis transaction ID for a given singleton
func (d *Database) GetGenesisTransactionBySingleton(
	singleton string,
) (string, error) {
	var contract Contract
	err := d.db.Where("singleton = ? AND genesis_confirmed = ?", singleton, false).
		First(&contract).
		Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return "", fmt.Errorf(
				"no unconfirmed contract found with singleton %s",
				singleton,
			)
		}
		return "", fmt.Errorf("failed to get genesis transaction: %w", err)
	}
	return contract.GenesisTx, nil
}
