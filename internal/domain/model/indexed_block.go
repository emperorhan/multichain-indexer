package model

import "time"

type IndexedBlock struct {
	Chain         Chain      `db:"chain"`
	Network       Network    `db:"network"`
	BlockNumber   int64      `db:"block_number"`
	BlockHash     string     `db:"block_hash"`
	ParentHash    string     `db:"parent_hash"`
	FinalityState string     `db:"finality_state"`
	BlockTime     *time.Time `db:"block_time"`
}
