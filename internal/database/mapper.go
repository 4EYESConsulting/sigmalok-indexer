package database

import (
	"fmt"

	"github.com/google/uuid"
)

func (d *Database) mapContract(c *Contract) (*ContractResponse, error) {
	resp := &ContractResponse{
		Singleton:           c.Singleton,
		BlockHeight:         c.BlockHeight,
		Name:                c.Name,
		Creator:             c.Creator,
		Benefactor:          c.Benefactor,
		Deadline:            c.Deadline,
		OracleNFT:           c.OracleNFT,
		OracleValue:         c.OracleValue,
		OracleIsGreaterThan: c.OracleIsGreaterThan,
		GenesisTx:           c.GenesisTx,
		GenesisErg:          c.GenesisErg,
		GenesisConfirmed:    c.GenesisConfirmed,
		Designates:          make([]DesignateInfo, len(c.Designates)),
		FundingTxs:          make([]FundingTxInfo, len(c.FundingTxs)),
		MintTxs:             make([]MintTxInfo, len(c.MintTxs)),
		RedeemTxs:           make([]RedeemTxInfo, len(c.RedeemTxs)),
		Value: ContractValue{
			Tokens: struct {
				Confirmed   map[string]TokenBalance `json:"confirmed"`
				Unconfirmed map[string]TokenBalance `json:"unconfirmed"`
			}{
				Confirmed:   make(map[string]TokenBalance),
				Unconfirmed: make(map[string]TokenBalance),
			},
		},
	}

	if c.FeeParameter.ID != uuid.Nil {
		resp.FeeHash = c.FeeParameter.FeeHash
		resp.FeeAmount = c.FeeParameter.FeeAmount
	}
	if c.Key != nil {
		resp.Key = &KeyInfo{ID: c.Key.ID, Amount: c.Key.Amount}
	}

	for i, dsg := range c.Designates {
		resp.Designates[i] = DesignateInfo{
			Address:     dsg.Address,
			BlockHeight: dsg.BlockHeight,
		}
	}
	for i, f := range c.FundingTxs {
		resp.FundingTxs[i] = FundingTxInfo{
			Tx:          f.Tx,
			ErgDelta:    f.ErgDelta,
			BlockHeight: f.BlockHeight,
			Confirmed:   f.Confirmed,
		}
	}
	for i, m := range c.MintTxs {
		resp.MintTxs[i] = MintTxInfo{
			Tx:          m.Tx,
			BlockHeight: m.BlockHeight,
			Confirmed:   m.Confirmed,
		}
	}
	for i, r := range c.RedeemTxs {
		resp.RedeemTxs[i] = RedeemTxInfo{
			Tx:               r.Tx,
			RecipientAddress: r.RecipientAddress,
			Type:             r.Type,
			BlockHeight:      r.BlockHeight,
			Confirmed:        r.Confirmed,
		}
	}

	val, err := d.aggregateContractValue(
		c.Singleton,
		c.GenesisConfirmed,
		c.GenesisErg,
		c.RedeemTxs,
	)
	if err != nil {
		return nil, fmt.Errorf("aggregate value for %s: %w", c.Singleton, err)
	}
	resp.Value = val

	return resp, nil
}
