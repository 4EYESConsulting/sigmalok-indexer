package types

type GetStatus struct{}

type Status struct {
	LastProcessedHeight uint64
	TipHeight           uint64
	PendingDownstream   int
	ReadersAlive        bool
}
