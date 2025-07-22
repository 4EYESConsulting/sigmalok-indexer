package database

import (
	"fmt"
)

func (d *Database) aggregateContractValue(
	contractID string,
	genesisConfirmed bool,
	genesisErg uint64,
	redeems []RedeemTx,
) (ContractValue, error) {
	var ergSum struct{ Confirmed, Unconfirmed uint64 }
	if err := d.db.Raw(`
        SELECT
          COALESCE(SUM(CASE WHEN block_height > 0 THEN erg_delta ELSE 0 END),0) AS confirmed,
          COALESCE(SUM(CASE WHEN block_height = 0 THEN erg_delta ELSE 0 END),0) AS unconfirmed
        FROM funding_tx
        WHERE contract_id = ?`, contractID,
	).Scan(&ergSum).Error; err != nil {
		return ContractValue{}, fmt.Errorf("aggregate ERG: %w", err)
	}

	if genesisConfirmed {
		ergSum.Confirmed += genesisErg
	} else {
		ergSum.Unconfirmed += genesisErg
	}

	type tokenRow struct {
		AssetID     string
		Confirmed   uint64
		Unconfirmed uint64
	}
	var rows []tokenRow
	if err := d.db.Raw(`
        SELECT asset_id,
               COALESCE(SUM(CASE WHEN block_height > 0 THEN delta ELSE 0 END),0) AS confirmed,
               COALESCE(SUM(CASE WHEN block_height = 0 THEN delta ELSE 0 END),0) AS unconfirmed
        FROM token_event
        WHERE contract_id = ?
        GROUP BY asset_id`, contractID,
	).Scan(&rows).Error; err != nil {
		return ContractValue{}, fmt.Errorf("aggregate token balances: %w", err)
	}

	cv := ContractValue{
		ERG: struct {
			Confirmed   uint64 `json:"confirmed"`
			Unconfirmed uint64 `json:"unconfirmed"`
		}{
			Confirmed:   ergSum.Confirmed,
			Unconfirmed: ergSum.Unconfirmed,
		},
		Tokens: struct {
			Confirmed   map[string]TokenBalance `json:"confirmed"`
			Unconfirmed map[string]TokenBalance `json:"unconfirmed"`
		}{
			Confirmed:   make(map[string]TokenBalance),
			Unconfirmed: make(map[string]TokenBalance),
		},
	}

	for _, r := range rows {
		if r.Confirmed > 0 {
			cv.Tokens.Confirmed[r.AssetID] = TokenBalance{
				TokenID: r.AssetID,
				Amount:  r.Confirmed,
			}
		}
		if r.Unconfirmed > 0 {
			cv.Tokens.Unconfirmed[r.AssetID] = TokenBalance{
				TokenID: r.AssetID,
				Amount:  r.Unconfirmed,
			}
		}
	}

	// zero out everything if any redeem is confirmed
	for _, r := range redeems {
		if r.Confirmed {
			cv.ERG = struct {
				Confirmed   uint64 `json:"confirmed"`
				Unconfirmed uint64 `json:"unconfirmed"`
			}{}
			cv.Tokens.Confirmed = map[string]TokenBalance{}
			cv.Tokens.Unconfirmed = map[string]TokenBalance{}
			break
		}
	}

	return cv, nil
}
