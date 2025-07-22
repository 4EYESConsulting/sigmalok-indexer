package database

import (
	"fmt"
	"math/rand"
)

const (
	maxCursorEntries = 50
)

type Cursor struct {
	ID          uint   `gorm:"primaryKey"`
	Hash        []byte `gorm:"type:blob;size:32"`
	Height      uint64 `gorm:"autoIncrement:false"`
	BlockOffset int64  `gorm:"autoIncrement:false"`
	TxOffset    int64  `gorm:"autoIncrement:false"`
	TxHash      []byte `gorm:"type:blob;size:32"`
}

type CursorPoint struct {
	Height      uint64 `yaml:"height"       json:"height"`
	Hash        []byte `yaml:"hash"         json:"hash"`
	BlockOffset int64  `yaml:"block_offset" json:"block_offset"`
	TxOffset    int64  `yaml:"tx_offset"    json:"tx_offset"`
	TxHash      []byte `yaml:"tx_hash"      json:"tx_hash"`
}

func (Cursor) TableName() string {
	return "cursor"
}

func (d *Database) AddCursorPoint(point CursorPoint) error {
	tmpItem := Cursor{
		Hash:        point.Hash,
		Height:      point.Height,
		BlockOffset: point.BlockOffset,
		TxOffset:    point.TxOffset,
		TxHash:      point.TxHash,
	}
	if result := d.db.Create(&tmpItem); result.Error != nil {
		return result.Error
	}
	// Remove older cursor entries
	// We do this approximately 1% of the time to reduce DB writes
	if rand.Intn(100) == 0 {
		result := d.db.
			Where("id < (SELECT max(id) FROM cursor) - ?", maxCursorEntries).
			Delete(&Cursor{})
		if result.Error != nil {
			return fmt.Errorf(
				"failure removing cursor entries: %s",
				result.Error,
			)
		}
	}
	return nil
}

func (d *Database) GetCursorPoints() ([]CursorPoint, error) {
	var cursorPoints []Cursor
	result := d.db.
		Order("id DESC").
		Find(&cursorPoints)
	if result.Error != nil {
		return nil, result.Error
	}
	ret := make([]CursorPoint, len(cursorPoints))
	for i, tmpPoint := range cursorPoints {
		ret[i] = CursorPoint{
			Hash:        tmpPoint.Hash,
			Height:      tmpPoint.Height,
			BlockOffset: tmpPoint.BlockOffset,
			TxOffset:    tmpPoint.TxOffset,
			TxHash:      tmpPoint.TxHash,
		}
	}
	return ret, nil
}

// RollbackCursor removes all cursor entries with heights greater than or equal to the provided height
func (d *Database) RollbackCursor(height uint64) error {
	result := d.db.Where("height >= ?", height).Delete(&Cursor{})
	if result.Error != nil {
		return fmt.Errorf("failed to rollback cursor entries: %w", result.Error)
	}
	return nil
}
