package database

// MigrateModels contains a list of database model types to perform automatic migrations
// on at startup
var MigrateModels = []any{
	&Cursor{},
	&Block{},
	&Address{},
	&FeeParameter{},
	&Contract{},
	&Key{},
	&TokenEvent{},
	&Designate{},
	&FundingTx{},
	&MintTx{},
	&RedeemTx{},
}
