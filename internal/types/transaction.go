package types

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/fxamacker/cbor/v2"
)

type Transaction struct {
	ID         string      `json:"id"`
	Inputs     []Input     `json:"inputs"`
	DataInputs []DataInput `json:"dataInputs"`
	Outputs    []Output    `json:"outputs"`
}

type Input struct {
	BoxID               string                `json:"boxId"`
	SpendingProof       SpendingProof         `json:"spendingProof"`
	Value               uint64                `json:"value"`
	ErgoTree            string                `json:"ergoTree"`
	Assets              []Asset               `json:"assets"`
	AdditionalRegisters NonMandatoryRegisters `json:"additionalRegisters"`
	CreationHeight      uint32                `json:"creationHeight"`
	TransactionID       string                `json:"transactionId"`
	Index               int                   `json:"index"`
}

type SpendingProof struct {
	ProofBytes string         `json:"proofBytes"`
	Extension  map[int]string `json:"extension"`
}

type DataInput struct {
	BoxID string `json:"boxId"`
}

type Output struct {
	BoxID               string                `json:"boxId"`
	Value               uint64                `json:"value"`
	ErgoTree            string                `json:"ergoTree"`
	Assets              []Asset               `json:"assets"`
	AdditionalRegisters NonMandatoryRegisters `json:"additionalRegisters"`
	CreationHeight      uint32                `json:"creationHeight"`
	TransactionID       string                `json:"transactionId"`
	Index               int                   `json:"index"`
}

type Asset struct {
	TokenID string `json:"tokenId"`
	Amount  uint64 `json:"amount"`
}

type NonMandatoryRegisters struct {
	R4 *string `json:"R4,omitempty"`
	R5 *string `json:"R5,omitempty"`
	R6 *string `json:"R6,omitempty"`
	R7 *string `json:"R7,omitempty"`
	R8 *string `json:"R8,omitempty"`
	R9 *string `json:"R9,omitempty"`
}

func ParseTransaction(jsonStr string) (*Transaction, error) {
	var tx Transaction
	if err := json.Unmarshal([]byte(jsonStr), &tx); err != nil {
		return nil, fmt.Errorf("failed to parse transaction JSON: %w", err)
	}
	return &tx, nil
}

// convertToStringKeyed recursively converts map[interface{}]interface{} to map[string]interface{}
func convertToStringKeyed(obj interface{}) interface{} {
	switch v := obj.(type) {
	case map[interface{}]interface{}:
		result := make(map[string]interface{})
		for key, value := range v {
			keyStr := fmt.Sprintf("%v", key)
			result[keyStr] = convertToStringKeyed(value)
		}
		return result
	case map[string]interface{}:
		result := make(map[string]interface{})
		for key, value := range v {
			result[key] = convertToStringKeyed(value)
		}
		return result
	case []interface{}:
		result := make([]interface{}, len(v))
		for i, item := range v {
			result[i] = convertToStringKeyed(item)
		}
		return result
	case []byte:
		return hex.EncodeToString(v)
	default:
		rv := reflect.ValueOf(obj)
		if rv.Kind() == reflect.Slice && rv.Type().Elem().Kind() == reflect.Uint8 {
			bytes := make([]byte, rv.Len())
			for i := 0; i < rv.Len(); i++ {
				bytes[i] = byte(rv.Index(i).Uint())
			}
			return hex.EncodeToString(bytes)
		}
		return obj
	}
}

func ParseCBORTransaction(txB64 string) (*Transaction, error) {
	if txB64 == "" {
		return nil, fmt.Errorf("empty transaction string")
	}

	cborBytes, err := base64.StdEncoding.DecodeString(txB64)
	if err != nil {
		return nil, fmt.Errorf("failed to decode base64: %w", err)
	}

	var txData interface{}
	if err := cbor.Unmarshal(cborBytes, &txData); err != nil {
		return nil, fmt.Errorf("failed to decode CBOR: %w", err)
	}

	convertedData := convertToStringKeyed(txData)

	jsonBytes, err := json.Marshal(convertedData)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal to JSON: %w", err)
	}

	tx, err := ParseTransaction(string(jsonBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to parse transaction JSON: %w", err)
	}

	return tx, nil
}
