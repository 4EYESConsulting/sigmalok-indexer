package strategy

import (
	"4EYESConsulting/sigmalok-indexer/internal/database"
	"4EYESConsulting/sigmalok-indexer/internal/types"
	"context"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	"github.com/mgpai22/ergo-connector-go/node"
	ergo "github.com/sigmaspace-io/ergo-lib-go"
)

const (
	expectedR4 = "SGroupElement"
	expectedR5 = "STuple([SColl(SByte), SLong])"
	expectedR6 = "STuple([SLong, SLong])"
	expectedR7 = "SColl(SGroupElement)"
	expectedR8 = "STuple([SColl(SByte), SBoolean])"
	expectedR9 = "STuple([SColl(SColl(SByte)), SLong])"

	createTokenLokActionType = "0402"
	fundLokActionType        = "0404"
	redeemLokActionType      = "0406"
)

const (
	GenesisAction = iota
	CreateTokenLokAction
	FundLokAction
	RedeemLokAction
)

func validateLokBox(box ergo.Box) bool {
	extractedR4, err := box.RegisterValue(ergo.R4)
	if extractedR4 == nil || err != nil {
		return false
	}

	r4Type, err := extractedR4.Type()
	if err != nil {
		return false
	}

	if r4Type != expectedR4 {
		return false
	}

	extractedR5, err := box.RegisterValue(ergo.R5)
	if extractedR5 == nil || err != nil {
		return false
	}

	r5Type, err := extractedR5.Type()
	if err != nil {
		return false
	}

	if r5Type != expectedR5 {
		return false
	}

	extractedR6, err := box.RegisterValue(ergo.R6)
	if extractedR6 == nil || err != nil {
		return false
	}

	r6Type, err := extractedR6.Type()
	if err != nil {
		return false
	}

	if r6Type != expectedR6 {
		return false
	}

	extractedR7, err := box.RegisterValue(ergo.R7)
	if extractedR7 == nil || err != nil {
		return false
	}

	r7Type, err := extractedR7.Type()
	if err != nil {
		return false
	}

	if r7Type != expectedR7 {
		return false
	}

	extractedR8, err := box.RegisterValue(ergo.R8)
	if extractedR8 == nil || err != nil {
		return false
	}

	r8Type, err := extractedR8.Type()
	if err != nil {
		return false
	}

	if r8Type != expectedR8 {
		return false
	}

	extractedR9, err := box.RegisterValue(ergo.R9)
	if extractedR9 == nil || err != nil {
		return false
	}

	r9Type, err := extractedR9.Type()
	if err != nil {
		return false
	}

	if r9Type != expectedR9 {
		return false
	}

	return true
}

// LokBoxRegisters holds the parsed values from all registers.
type LokBoxRegisters struct {
	R4      string     // Hex string (SGroupElement)
	R5Bytes string     // Hex string (SColl(SByte))
	R5Long  int64      // SLong
	R6Long1 int64      // First SLong
	R6Long2 int64      // Second SLong
	R7      []string   // Array of hex strings (SColl(SGroupElement))
	R8Bytes string     // Hex string (SColl(SByte))
	R8Bool  bool       // SBoolean
	R9Colls [][]string // Array of hex string arrays (SColl(SColl(SByte)))
	R9Long  int64      // SLong
}

// parseR4Value parses R4 string (e.g., "EC:hex") into hex string.
func parseR4Value(r4Str string) (string, error) {
	if len(r4Str) > 3 && strings.HasPrefix(r4Str, "EC:") {
		hexStr := r4Str[3:]
		// Validate it's valid hex (optional, but adds safety).
		if _, err := hex.DecodeString(hexStr); err != nil {
			return "", fmt.Errorf("invalid hex in R4: %w", err)
		}
		return hexStr, nil
	}
	return "", fmt.Errorf("invalid R4 format: %s", r4Str)
}

// parseTupleBoundedVec parses a BoundedVec string into a slice of strings
// (handles quoted items, numbers, booleans, nested arrays).
func parseTupleBoundedVec(vecStr string) ([]string, error) {
	if !strings.HasPrefix(vecStr, "BoundedVec{inner:[") ||
		!strings.HasSuffix(vecStr, "]}") {
		return nil, fmt.Errorf("invalid BoundedVec format: %s", vecStr)
	}
	inner := strings.TrimPrefix(vecStr, "BoundedVec{inner:[")
	inner = strings.TrimSuffix(inner, "]}")

	// Split by top-level commas, handling nested arrays.
	var parts []string
	var depth, start int
	for i, ch := range inner {
		switch ch {
		case '[':
			depth++
		case ']':
			depth--
		case ',':
			if depth == 0 {
				parts = append(parts, inner[start:i])
				start = i + 1
			}
		}
	}
	if start < len(inner) {
		parts = append(parts, inner[start:])
	}
	return parts, nil
}

// parseR5Value parses R5 BoundedVec into (bytesHex, long).
func parseR5Value(r5Str string) (string, int64, error) {
	parts, err := parseTupleBoundedVec(r5Str)
	if err != nil || len(parts) != 2 {
		return "", 0, fmt.Errorf("invalid R5 parts: %s (%v)", r5Str, err)
	}
	bytesHex := strings.Trim(parts[0], "\"")
	if _, err := hex.DecodeString(bytesHex); err != nil {
		return "", 0, fmt.Errorf("invalid hex in R5: %w", err)
	}
	longVal, err := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)
	if err != nil {
		return "", 0, fmt.Errorf("invalid long in R5: %w", err)
	}
	return bytesHex, longVal, nil
}

// parseR6Value parses R6 BoundedVec into (long1, long2).
func parseR6Value(r6Str string) (int64, int64, error) {
	parts, err := parseTupleBoundedVec(r6Str)
	if err != nil || len(parts) != 2 {
		return 0, 0, fmt.Errorf("invalid R6 parts: %s (%v)", r6Str, err)
	}
	long1, err := strconv.ParseInt(strings.TrimSpace(parts[0]), 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid first long in R6: %w", err)
	}
	long2, err := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid second long in R6: %w", err)
	}
	return long1, long2, nil
}

// parseR7Value parses R7 array string (e.g., "[EC:hex1,EC:hex2]") into []string hex.
func parseR7Value(r7Str string) ([]string, error) {
	if !strings.HasPrefix(r7Str, "[") || !strings.HasSuffix(r7Str, "]") {
		return nil, fmt.Errorf("invalid R7 format: %s", r7Str)
	}
	inner := strings.TrimPrefix(r7Str, "[")
	inner = strings.TrimSuffix(inner, "]")
	if inner == "" {
		return []string{}, nil
	}

	items := strings.Split(inner, ",")
	var parsed []string
	for _, item := range items {
		item = strings.TrimSpace(item)
		if len(item) > 3 && strings.HasPrefix(item, "EC:") {
			hexStr := item[3:]
			if _, err := hex.DecodeString(hexStr); err != nil {
				return nil, fmt.Errorf("invalid hex in R7: %w", err)
			}
			parsed = append(parsed, hexStr)
		} else {
			return nil, fmt.Errorf("invalid EC item in R7: %s", item)
		}
	}
	return parsed, nil
}

// parseR8Value parses R8 BoundedVec into (bytesHex, bool).
func parseR8Value(r8Str string) (string, bool, error) {
	parts, err := parseTupleBoundedVec(r8Str)
	if err != nil || len(parts) != 2 {
		return "", false, fmt.Errorf("invalid R8 parts: %s (%v)", r8Str, err)
	}
	bytesHex := strings.Trim(parts[0], "\"")
	// Allow empty string as valid (as per log).
	if bytesHex != "" {
		if _, err := hex.DecodeString(bytesHex); err != nil {
			return "", false, fmt.Errorf("invalid hex in R8: %w", err)
		}
	}
	boolStr := strings.TrimSpace(parts[1])
	boolVal, err := strconv.ParseBool(boolStr)
	if err != nil {
		return "", false, fmt.Errorf("invalid bool in R8: %w", err)
	}
	return bytesHex, boolVal, nil
}

// parseR9Value parses R9 BoundedVec into ([][]string hex arrays, long).
func parseR9Value(r9Str string) ([][]string, int64, error) {
	parts, err := parseTupleBoundedVec(r9Str)
	if err != nil || len(parts) != 2 {
		return nil, 0, fmt.Errorf("invalid R9 parts: %s (%v)", r9Str, err)
	}

	// Parse nested collections (e.g., [["hex1","hex2"]]).
	collStr := strings.TrimSpace(parts[0])
	if !strings.HasPrefix(collStr, "[") || !strings.HasSuffix(collStr, "]") {
		return nil, 0, fmt.Errorf("invalid R9 collections format: %s", collStr)
	}
	innerColl := strings.TrimPrefix(collStr, "[")
	innerColl = strings.TrimSuffix(innerColl, "]")

	var colls [][]string
	if innerColl != "" {
		// Split inner arrays (assuming one level of nesting as per type/log).
		nestedParts := strings.Split(innerColl, "],[")
		for _, nested := range nestedParts {
			nested = strings.Trim(nested, "[]")
			items := strings.Split(nested, ",")
			var parsedInner []string
			for _, item := range items {
				hexStr := strings.Trim(item, "\"")
				if hexStr != "" {
					if _, err := hex.DecodeString(hexStr); err != nil {
						return nil, 0, fmt.Errorf("invalid hex in R9: %w", err)
					}
					parsedInner = append(parsedInner, hexStr)
				}
			}
			colls = append(colls, parsedInner)
		}
	}

	longVal, err := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)
	if err != nil {
		return nil, 0, fmt.Errorf("invalid long in R9: %w", err)
	}
	return colls, longVal, nil
}

func extractLokBoxRegisterValues(box ergo.Box) (*LokBoxRegisters, error) {
	regs := &LokBoxRegisters{}

	extractedR4, err := box.RegisterValue(ergo.R4)
	if extractedR4 == nil || err != nil {
		return nil, err
	}
	r4Value, err := extractedR4.Value()
	if err != nil {
		return nil, err
	}
	regs.R4, err = parseR4Value(r4Value)
	if err != nil {
		return nil, err
	}

	extractedR5, err := box.RegisterValue(ergo.R5)
	if extractedR5 == nil || err != nil {
		return nil, err
	}
	r5Value, err := extractedR5.Value()
	if err != nil {
		return nil, err
	}
	regs.R5Bytes, regs.R5Long, err = parseR5Value(r5Value)
	if err != nil {
		return nil, err
	}

	extractedR6, err := box.RegisterValue(ergo.R6)
	if extractedR6 == nil || err != nil {
		return nil, err
	}
	r6Value, err := extractedR6.Value()
	if err != nil {
		return nil, err
	}
	regs.R6Long1, regs.R6Long2, err = parseR6Value(r6Value)
	if err != nil {
		return nil, err
	}

	extractedR7, err := box.RegisterValue(ergo.R7)
	if extractedR7 == nil || err != nil {
		return nil, err
	}
	r7Value, err := extractedR7.Value()
	if err != nil {
		return nil, err
	}
	regs.R7, err = parseR7Value(r7Value)
	if err != nil {
		return nil, err
	}

	extractedR8, err := box.RegisterValue(ergo.R8)
	if extractedR8 == nil || err != nil {
		return nil, err
	}
	r8Value, err := extractedR8.Value()
	if err != nil {
		return nil, err
	}
	regs.R8Bytes, regs.R8Bool, err = parseR8Value(r8Value)
	if err != nil {
		return nil, err
	}

	extractedR9, err := box.RegisterValue(ergo.R9)
	if extractedR9 == nil || err != nil {
		return nil, err
	}
	r9Value, err := extractedR9.Value()
	if err != nil {
		return nil, err
	}
	regs.R9Colls, regs.R9Long, err = parseR9Value(r9Value)
	if err != nil {
		return nil, err
	}

	return regs, nil
}

func tryMatchLokBoxAction(
	currentBlockHeight uint64,
	txInput types.Input,
	nodeProvider *node.NodeProvider,
) int {
	if nodeProvider == nil {
		return -1
	}

	var contextExtension string
	var spendingProof *node.SpendingProof
	var err error

	if currentBlockHeight == 0 {
		spendingProof, err = nodeProvider.GetSpendingProofByUnconfirmedBoxId(
			context.Background(),
			txInput.BoxID,
		)
	} else {
		spendingProof, err = nodeProvider.GetSpendingProofByBoxId(
			context.Background(),
			txInput.BoxID,
			int32(currentBlockHeight),
		)
	}

	if err != nil {
		return -1
	}

	if spendingProof.Extension != nil {
		if contextExt, exists := spendingProof.Extension["0"]; exists {
			contextExtension = string(contextExt)
		}
	}

	// If no context extensions found, it's genesis
	if contextExtension == "" {
		return GenesisAction
	}

	switch contextExtension {
	case createTokenLokActionType:
		return CreateTokenLokAction
	case fundLokActionType:
		return FundLokAction
	case redeemLokActionType:
		return RedeemLokAction
	default:
		return -1
	}
}

func matchRedeemLokAction(
	currentBlockHeight uint64,
	deadlineHeight uint64,
	txInputs []types.Input,
	designates []string,
) database.RedeemType {
	if currentBlockHeight > deadlineHeight {
		return database.RedeemTypeDeadlineReached
	}

	for _, designate := range designates {
		if txInputs[1].ErgoTree == designate {
			return database.RedeemTypeDesignateRedeem
		}
	}

	return database.RedeemTypeOracleRedeem
}
