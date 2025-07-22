package database

import (
	"errors"
	"fmt"

	"gorm.io/gorm"
)

type TokenBalance struct {
	TokenID string `json:"tokenId"`
	Amount  uint64 `json:"amount"`
}

type ContractValue struct {
	ERG struct {
		Confirmed   uint64 `json:"confirmed"`
		Unconfirmed uint64 `json:"unconfirmed"`
	} `json:"erg"`
	Tokens struct {
		Confirmed   map[string]TokenBalance `json:"confirmed"`
		Unconfirmed map[string]TokenBalance `json:"unconfirmed"`
	} `json:"tokens"`
}

type KeyInfo struct {
	ID     string `json:"id"`
	Amount uint64 `json:"amount"`
}

type DesignateInfo struct {
	Address     string `json:"address"`
	BlockHeight uint64 `json:"block_height"`
}

type FundingTxInfo struct {
	Tx          string `json:"tx"`
	ErgDelta    uint64 `json:"erg_delta"`
	BlockHeight uint64 `json:"block_height"`
	Confirmed   bool   `json:"confirmed"`
}

type MintTxInfo struct {
	Tx          string `json:"tx"`
	BlockHeight uint64 `json:"block_height"`
	Confirmed   bool   `json:"confirmed"`
}

type RedeemTxInfo struct {
	Tx               string     `json:"tx"`
	RecipientAddress string     `json:"recipient_address"`
	Type             RedeemType `json:"type"`
	BlockHeight      uint64     `json:"block_height"`
	Confirmed        bool       `json:"confirmed"`
}

type ContractResponse struct {
	Singleton           string          `json:"singleton"`
	BlockHeight         uint64          `json:"block_height"`
	Name                *string         `json:"name"`
	Creator             string          `json:"creator"`
	Benefactor          string          `json:"benefactor"`
	Deadline            uint64          `json:"deadline"`
	OracleNFT           *string         `json:"oracle_nft"`
	OracleValue         *uint64         `json:"oracle_value"`
	OracleIsGreaterThan *bool           `json:"oracle_is_greater_than"`
	GenesisTx           string          `json:"genesis_tx"`
	GenesisErg          uint64          `json:"genesis_erg"`
	GenesisConfirmed    bool            `json:"genesis_confirmed"`
	FeeHash             string          `json:"fee_hash"`
	FeeAmount           int             `json:"fee_amount"`
	Key                 *KeyInfo        `json:"key"`
	Designates          []DesignateInfo `json:"designates"`
	FundingTxs          []FundingTxInfo `json:"funding_txs"`
	MintTxs             []MintTxInfo    `json:"mint_txs"`
	RedeemTxs           []RedeemTxInfo  `json:"redeem_txs"`
	Value               ContractValue   `json:"value"`
}

func withContractPreloads(db *gorm.DB) *gorm.DB {
	return db.
		Preload("Key").
		Preload("Designates").
		Preload("FundingTxs").
		Preload("MintTxs").
		Preload("RedeemTxs").
		Preload("FeeParameter")
}

func (d *Database) loadAndMapContracts(
	where string,
	limit, offset int,
	args ...interface{},
) ([]ContractResponse, error) {
	var cs []Contract
	if err := d.db.
		Scopes(withContractPreloads).
		Where(where, args...).
		Limit(limit).
		Offset(offset).
		Find(&cs).Error; err != nil {
		return nil, fmt.Errorf("failed to get contracts: %w", err)
	}

	out := make([]ContractResponse, len(cs))
	for i := range cs {
		resp, err := d.mapContract(&cs[i])
		if err != nil {
			return nil, fmt.Errorf(
				"mapping contract %s: %w",
				cs[i].Singleton,
				err,
			)
		}
		out[i] = *resp
	}
	return out, nil
}

func (d *Database) GetContractBySingleton(
	singleton string,
) (*ContractResponse, error) {
	var c Contract
	err := d.db.
		Scopes(withContractPreloads).
		Where("singleton = ?", singleton).
		First(&c).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get contract: %w", err)
	}
	return d.mapContract(&c)
}

func (d *Database) GetContractsByCreatorAddress(
	creator string,
	limit, offset int,
) ([]ContractResponse, error) {
	return d.loadAndMapContracts("creator = ?", limit, offset, creator)
}

func (d *Database) GetContractsByBenefactorAddress(
	benefactor string,
	limit, offset int,
) ([]ContractResponse, error) {
	return d.loadAndMapContracts("benefactor = ?", limit, offset, benefactor)
}

func (d *Database) GetContractsBeforeOrOnDeadline(
	deadline uint64,
	limit, offset int,
) ([]ContractResponse, error) {
	return d.loadAndMapContracts("deadline <= ?", limit, offset, deadline)
}

func (d *Database) GetContractsAfterDeadline(
	deadline uint64,
	limit, offset int,
) ([]ContractResponse, error) {
	return d.loadAndMapContracts("deadline > ?", limit, offset, deadline)
}

func (d *Database) GetContractsByKeys(
	keys []string,
	limit, offset int,
) ([]ContractResponse, error) {
	return d.loadAndMapContracts(
		"EXISTS (SELECT 1 FROM key WHERE key.contract_id = contract.singleton AND key.id IN ?)",
		limit,
		offset,
		keys,
	)
}

func (d *Database) GetContractsByDesignateAddresses(
	addresses []string,
	limit, offset int,
) ([]ContractResponse, error) {
	return d.loadAndMapContracts(
		"EXISTS (SELECT 1 FROM designate WHERE designate.contract_id = contract.singleton AND designate.address IN ?)",
		limit,
		offset,
		addresses,
	)
}

func (d *Database) GetAllContracts(
	limit, offset int,
) ([]ContractResponse, error) {
	return d.loadAndMapContracts("1=1", limit, offset)
}

func (d *Database) GetTotalContractCount() (int64, error) {
	var count int64
	if err := d.db.Model(&Contract{}).Count(&count).Error; err != nil {
		return 0, fmt.Errorf("count contracts: %w", err)
	}
	return count, nil
}

func (d *Database) GetTotalLockedErg() (uint64, error) {
	var out struct{ Total uint64 }
	q := `
WITH funding_sum AS (
    SELECT contract_id,
           SUM(CASE WHEN block_height > 0 THEN erg_delta ELSE 0 END) AS erg_funding
    FROM funding_tx
    GROUP BY contract_id
)
SELECT COALESCE(SUM(
        CASE WHEN c.genesis_confirmed THEN c.genesis_erg ELSE 0 END +
        COALESCE(f.erg_funding, 0)
), 0) AS total
FROM contract c
LEFT JOIN funding_sum f ON f.contract_id = c.singleton
WHERE NOT EXISTS (
    SELECT 1 FROM redeem_tx r
    WHERE r.contract_id = c.singleton
      AND r.confirmed = TRUE
);`
	if err := d.db.Raw(q).Scan(&out).Error; err != nil {
		return 0, fmt.Errorf("sum locked ERG: %w", err)
	}
	return out.Total, nil
}

func (d *Database) GetTotalUniqueTokenCount() (int64, error) {
	var cnt int64
	q := `
SELECT COUNT(DISTINCT t.asset_id)
FROM token_event t
WHERE t.block_height > 0
  AND NOT EXISTS (
        SELECT 1 FROM redeem_tx r
        WHERE r.contract_id = t.contract_id
          AND r.confirmed = TRUE
);`
	if err := d.db.Raw(q).Scan(&cnt).Error; err != nil {
		return 0, fmt.Errorf("count unique tokens: %w", err)
	}
	return cnt, nil
}

func (d *Database) GetTotalClaimableErgAfterHeight(
	height uint64,
) (uint64, error) {
	var out struct{ Total uint64 }
	q := `
WITH funding_sum AS (
    SELECT contract_id,
           SUM(CASE WHEN block_height > 0 THEN erg_delta ELSE 0 END) AS erg_funding
    FROM funding_tx
    GROUP BY contract_id
)
SELECT COALESCE(SUM(
        CASE WHEN c.genesis_confirmed THEN c.genesis_erg ELSE 0 END +
        COALESCE(f.erg_funding, 0)
), 0) AS total
FROM contract c
LEFT JOIN funding_sum f ON f.contract_id = c.singleton
WHERE c.deadline <= ?
  AND NOT EXISTS (
        SELECT 1 FROM redeem_tx r
        WHERE r.contract_id = c.singleton
          AND r.confirmed = TRUE
);`
	if err := d.db.Raw(q, height).Scan(&out).Error; err != nil {
		return 0, fmt.Errorf(
			"sum claimable ERG after height %d: %w",
			height,
			err,
		)
	}
	return out.Total, nil
}

func (d *Database) GetTotalRedeemCount() (int64, error) {
	var count int64
	if err := d.db.Model(&RedeemTx{}).Where("confirmed = true").Count(&count).Error; err != nil {
		return 0, fmt.Errorf("count redeems: %w", err)
	}
	return count, nil
}
