package balances

import (
	"exchange/shm"
	"maps"
	"time"
)

var (
	BalanceUpdatesCh = make(chan shm.BalanceResponse, 1024)
	HoldingUpdatesCh = make(chan shm.HoldingResponse, 1024)
)

func StateUpdater() {
	// Start from initial snapshot
	snap := currentSnapshot.Load()
	balances := maps.Clone(snap.Balances)
	holdings := maps.Clone(snap.Holdings)

	// How often to publish a fresh snapshot
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case br := <-BalanceUpdatesCh:
			// Update balance map
			balances[br.UserId] = br.Response

		case hr := <-HoldingUpdatesCh:
			holdings[hr.UserId] = hr.Response

		case <-ticker.C:
			// Publish new immutable snapshot for readers
			newSnap := &StateSnapshot{
				Balances: balances,
				Holdings: holdings,
			}
			currentSnapshot.Store(newSnap)

			// Work on fresh copies so readers never see partially mutated maps
			balances = maps.Clone(balances)
			holdings = maps.Clone(holdings)
		}
	}
}
