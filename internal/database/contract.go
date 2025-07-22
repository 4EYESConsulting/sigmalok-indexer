package database

import (
	"github.com/google/uuid"
)

// RedeemType represents the enum for redeem types
type RedeemType string

const (
	RedeemTypeDeadlineReached RedeemType = "deadline_reached"
	RedeemTypeOracleRedeem    RedeemType = "oracle_redeem"
	RedeemTypeDesignateRedeem RedeemType = "designate_redeem"
)

type Block struct {
	Height    int    `gorm:"primaryKey;type:int"       json:"height"`
	Hash      string `gorm:"type:text;unique;not null" json:"hash"`
	Timestamp int64  `gorm:"not null"                  json:"timestamp"`
}

func (Block) TableName() string {
	return "block"
}

type Address struct {
	Address string `gorm:"primaryKey;type:text" json:"address"`
}

func (Address) TableName() string {
	return "address"
}

type FeeParameter struct {
	ID        uuid.UUID `gorm:"primaryKey;type:uuid" json:"id"`
	FeeHash   string    `gorm:"type:text;not null"   json:"fee_hash"`
	FeeAmount int       `gorm:"not null"             json:"fee_amount"`
}

func (FeeParameter) TableName() string {
	return "fee_parameter"
}

type Contract struct {
	Singleton           string    `gorm:"primaryKey;type:text" json:"singleton"`
	BlockHeight         uint64    `gorm:"not null"             json:"block_height"`
	BlockHash           string    `gorm:"type:text;not null"   json:"block_hash"`
	Name                *string   `gorm:"type:text"            json:"name"`
	Creator             string    `gorm:"type:text;not null"   json:"creator"`
	Benefactor          string    `gorm:"type:text;not null"   json:"benefactor"`
	Deadline            uint64    `gorm:"not null"             json:"deadline"`
	OracleNFT           *string   `gorm:"type:text"            json:"oracle_nft"`
	OracleValue         *uint64   `                            json:"oracle_value"`
	OracleIsGreaterThan *bool     `                            json:"oracle_is_greater_than"`
	GenesisTx           string    `gorm:"type:text;not null"   json:"genesis_tx"`
	GenesisErg          uint64    `gorm:"not null"             json:"genesis_erg"`
	ParameterID         uuid.UUID `gorm:"type:uuid;not null"   json:"parameter_id"`
	GenesisConfirmed    bool      `gorm:"not null"             json:"genesis_confirmed"`

	// Foreign key relationships
	Block             Block        `gorm:"foreignKey:BlockHeight;references:Height;constraint:OnDelete:CASCADE" json:"-"`
	CreatorAddress    Address      `gorm:"foreignKey:Creator;references:Address"                                json:"-"`
	BenefactorAddress Address      `gorm:"foreignKey:Benefactor;references:Address"                             json:"-"`
	FeeParameter      FeeParameter `gorm:"foreignKey:ParameterID;references:ID"                                 json:"-"`

	// Associations
	Key         *Key         `gorm:"foreignKey:ContractID;references:Singleton" json:"key,omitempty"`
	TokenEvents []TokenEvent `gorm:"foreignKey:ContractID;references:Singleton" json:"token_events,omitempty"`
	Designates  []Designate  `gorm:"foreignKey:ContractID;references:Singleton" json:"designates,omitempty"`
	FundingTxs  []FundingTx  `gorm:"foreignKey:ContractID;references:Singleton" json:"funding_txs,omitempty"`
	MintTxs     []MintTx     `gorm:"foreignKey:ContractID;references:Singleton" json:"mint_txs,omitempty"`
	RedeemTxs   []RedeemTx   `gorm:"foreignKey:ContractID;references:Singleton" json:"redeem_txs,omitempty"`
}

func (Contract) TableName() string {
	return "contract"
}

type Key struct {
	ContractID  string `gorm:"type:text;not null;unique" json:"contract_id"`
	ID          string `gorm:"primaryKey;type:text"      json:"id"`
	Amount      uint64 `gorm:"not null"                  json:"amount"`
	BlockHeight uint64 `gorm:"not null"                  json:"block_height"`

	// Foreign key relationships
	Contract Contract `gorm:"foreignKey:ContractID;references:Singleton;constraint:OnDelete:CASCADE" json:"-"`
	Block    Block    `gorm:"foreignKey:BlockHeight;references:Height;constraint:OnDelete:CASCADE"   json:"-"`
}

func (Key) TableName() string {
	return "key"
}

type TokenEvent struct {
	ID          uuid.UUID `gorm:"primaryKey;type:uuid" json:"id"`
	ContractID  string    `gorm:"type:text;not null"   json:"contract_id"`
	Tx          string    `gorm:"type:text;not null"   json:"tx"`
	AssetID     string    `gorm:"type:text;not null"   json:"asset_id"`
	Delta       uint64    `gorm:"not null"             json:"delta"`
	BlockHeight uint64    `gorm:"not null"             json:"block_height"`

	// Foreign key relationships
	Contract Contract `gorm:"foreignKey:ContractID;references:Singleton;constraint:OnDelete:CASCADE" json:"-"`
	Block    Block    `gorm:"foreignKey:BlockHeight;references:Height;constraint:OnDelete:CASCADE"   json:"-"`
}

func (TokenEvent) TableName() string {
	return "token_event"
}

type Designate struct {
	ContractID  string `gorm:"primaryKey;type:text;not null" json:"contract_id"`
	Address     string `gorm:"primaryKey;type:text;not null" json:"address"`
	BlockHeight uint64 `gorm:"not null"                      json:"block_height"`

	// Foreign key relationships
	Contract        Contract `gorm:"foreignKey:ContractID;references:Singleton;constraint:OnDelete:CASCADE" json:"-"`
	AddressRelation Address  `gorm:"foreignKey:Address;references:Address"                                  json:"-"`
	Block           Block    `gorm:"foreignKey:BlockHeight;references:Height;constraint:OnDelete:CASCADE"   json:"-"`
}

func (Designate) TableName() string {
	return "designate"
}

type FundingTx struct {
	ContractID  string `gorm:"primaryKey;type:text;not null" json:"contract_id"`
	Tx          string `gorm:"primaryKey;type:text;not null" json:"tx"`
	ErgDelta    uint64 `gorm:"not null"                      json:"erg_delta"`
	BlockHeight uint64 `gorm:"not null"                      json:"block_height"`
	WrittenAt   int64  `gorm:"not null"                      json:"timestamp"`
	Confirmed   bool   `gorm:"not null"                      json:"confirmed"`

	// Foreign key relationships
	Contract Contract `gorm:"foreignKey:ContractID;references:Singleton;constraint:OnDelete:CASCADE" json:"-"`
	Block    Block    `gorm:"foreignKey:BlockHeight;references:Height;constraint:OnDelete:CASCADE"   json:"-"`
}

func (FundingTx) TableName() string {
	return "funding_tx"
}

type MintTx struct {
	ContractID  string `gorm:"primaryKey;type:text;not null" json:"contract_id"`
	Tx          string `gorm:"type:text;not null"            json:"tx"`
	BlockHeight uint64 `gorm:"not null"                      json:"block_height"`
	Confirmed   bool   `gorm:"not null"                      json:"confirmed"`
	WrittenAt   int64  `gorm:"not null"                      json:"timestamp"`

	// Foreign key relationships
	Contract Contract `gorm:"foreignKey:ContractID;references:Singleton;constraint:OnDelete:CASCADE" json:"-"`
	Block    Block    `gorm:"foreignKey:BlockHeight;references:Height;constraint:OnDelete:CASCADE"   json:"-"`
}

func (MintTx) TableName() string {
	return "mint_tx"
}

type RedeemTx struct {
	ContractID       string     `gorm:"primaryKey;type:text;not null" json:"contract_id"`
	Tx               string     `gorm:"type:text;not null"            json:"tx"`
	RecipientAddress string     `gorm:"type:text;not null"            json:"recipient_address"`
	Type             RedeemType `gorm:"not null"                      json:"type"`
	BlockHeight      uint64     `gorm:"not null"                      json:"block_height"`
	Confirmed        bool       `gorm:"not null"                      json:"confirmed"`
	WrittenAt        int64      `gorm:"not null"                      json:"timestamp"`

	// Foreign key relationships
	Contract                 Contract `gorm:"foreignKey:ContractID;references:Singleton;constraint:OnDelete:CASCADE" json:"-"`
	RecipientAddressRelation Address  `gorm:"foreignKey:RecipientAddress;references:Address"                         json:"-"`
	Block                    Block    `gorm:"foreignKey:BlockHeight;references:Height;constraint:OnDelete:CASCADE"   json:"-"`
}

func (RedeemTx) TableName() string {
	return "redeem_tx"
}
