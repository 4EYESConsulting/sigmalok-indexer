package strategy

import (
	"4EYESConsulting/sigmalok-indexer/internal/database"
	"4EYESConsulting/sigmalok-indexer/internal/types"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	ergo "github.com/sigmaspace-io/ergo-lib-go"
)

var ErrNotLokBox = errors.New("box is not a valid LokBox")

type TxContext struct {
	BlockHeight uint64
	BlockHash   string
	Timestamp   time.Time
	Confirmed   bool
	TipHeight   uint64
}

func parseAndValidateLokBox(
	outputJSON []byte,
) (ergo.Box, *LokBoxRegisters, error) {
	box, err := ergo.NewBoxFromJson(string(outputJSON))
	if err != nil {
		var emptyBox ergo.Box
		return emptyBox, nil, fmt.Errorf("box json → Box: %w", err)
	}

	if !validateLokBox(box) {
		var emptyBox ergo.Box
		return emptyBox, nil, ErrNotLokBox
	}

	regs, err := extractLokBoxRegisterValues(box)
	if err != nil {
		var emptyBox ergo.Box
		return emptyBox, nil, fmt.Errorf("extract regs: %w", err)
	}

	return box, regs, nil
}

func parseAndValidateLokBoxFromOutput(
	tx *types.Transaction,
	idx int,
) (ergo.Box, *LokBoxRegisters, error) {
	if idx >= len(tx.Outputs) {
		var emptyBox ergo.Box
		return emptyBox, nil, fmt.Errorf("output index %d out of range", idx)
	}

	outputJSON, err := json.Marshal(tx.Outputs[idx])
	if err != nil {
		var emptyBox ergo.Box
		return emptyBox, nil, fmt.Errorf(
			"failed to marshal output to JSON: %w",
			err,
		)
	}

	return parseAndValidateLokBox(outputJSON)
}

func parseAndValidateLokBoxFromInput(
	tx *types.Transaction,
	idx int,
) (ergo.Box, *LokBoxRegisters, error) {
	if idx >= len(tx.Inputs) {
		var emptyBox ergo.Box
		return emptyBox, nil, fmt.Errorf("input index %d out of range", idx)
	}

	inputJSON, err := json.Marshal(tx.Inputs[idx])
	if err != nil {
		var emptyBox ergo.Box
		return emptyBox, nil, fmt.Errorf(
			"failed to marshal input to JSON: %w",
			err,
		)
	}

	return parseAndValidateLokBox(inputJSON)
}

func decodePkHexToBase58(hexStr string) (string, error) {
	pkBytes, err := hex.DecodeString(hexStr)
	if err != nil {
		return "", fmt.Errorf("failed to decode hex: %w", err)
	}

	addr, err := ergo.NewAddressFromPublicKey(pkBytes)
	if err != nil {
		return "", fmt.Errorf("failed to create address: %w", err)
	}

	return addr.Base58(ergo.MainnetPrefix), nil
}

func makeTokenEvents(outBox ergo.Box, inBox *ergo.Box) []database.TokenEvent {
	inMap := make(map[string]uint64)
	if inBox != nil {
		for i, t := range (*inBox).Tokens().All() {
			if i == 0 { // Skip singleton
				continue
			}
			inMap[t.Id().Base16()] = uint64(t.Amount().Int64())
		}
	}

	var events []database.TokenEvent
	for i, t := range outBox.Tokens().All() {
		if i == 0 { // Skip singleton
			continue
		}

		id := t.Id().Base16()
		out := uint64(t.Amount().Int64())
		in := inMap[id]

		var delta uint64
		if in > 0 { // funding case
			if out < in {
				continue // Skip decreases in funding
			}
			delta = out - in
		} else { // genesis case
			delta = out
		}

		if delta > 0 {
			events = append(events, database.TokenEvent{
				ID:      uuid.New(),
				AssetID: id,
				Delta:   delta,
			})
		}
	}

	return events
}

func getSingleton(box ergo.Box) (string, error) {
	tokens := box.Tokens()
	singletonToken, err := tokens.Get(0)
	if err != nil {
		return "", fmt.Errorf("failed to get singleton token: %w", err)
	}
	return singletonToken.Id().Base16(), nil
}

func extractDesignates(designateHexes []string) ([]database.Designate, error) {
	dbDesignates := make([]database.Designate, 0, len(designateHexes))

	for _, designate := range designateHexes {
		addressBase58, err := decodePkHexToBase58(designate)
		if err != nil {
			return nil, fmt.Errorf("failed to decode designate: %w", err)
		}

		dbDesignates = append(dbDesignates, database.Designate{
			Address: addressBase58,
		})
	}

	return dbDesignates, nil
}

func extractDesignatesErgoTrees(designateHexes []string) ([]string, error) {
	ergoTrees := make([]string, 0, len(designateHexes))

	for _, designate := range designateHexes {
		pkBytes, err := hex.DecodeString(designate)
		if err != nil {
			return nil, fmt.Errorf(
				"failed to decode designate public key: %w",
				err,
			)
		}

		designateAddress, err := ergo.NewAddressFromPublicKey(pkBytes)
		if err != nil {
			return nil, fmt.Errorf(
				"failed to create designate address: %w",
				err,
			)
		}

		ergoTree, err := designateAddress.Tree().Base16()
		if err != nil {
			return nil, fmt.Errorf("failed to get designate ergo tree: %w", err)
		}

		ergoTrees = append(ergoTrees, ergoTree)
	}

	return ergoTrees, nil
}

func getRecipientAddress(
	tx *types.Transaction,
	outputIndex int,
) (string, error) {
	if outputIndex >= len(tx.Outputs) {
		return "", fmt.Errorf("output index %d out of range", outputIndex)
	}

	recipientErgoTree, err := ergo.NewTree(tx.Outputs[outputIndex].ErgoTree)
	if err != nil {
		return "", fmt.Errorf("failed to decode recipient ergo tree: %w", err)
	}

	recipientAddressObject, err := ergo.NewAddressFromTree(recipientErgoTree)
	if err != nil {
		return "", fmt.Errorf("failed to create recipient address: %w", err)
	}

	return recipientAddressObject.Base58(ergo.MainnetPrefix), nil
}
