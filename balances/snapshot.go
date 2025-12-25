package balances
import (
	"exchange/shm"
	"sync/atomic"
)


// this is what we will use to maintain a consistent view of the balances and holdings
type StateSnapshot struct {
    Balances  map[uint64]shm.UserBalance
    Holdings  map[uint64]shm.UserHoldings
}
//poitner to the current snapshot will be mainteained and read by handler functions
var currentSnapshot atomic.Pointer[StateSnapshot]

func InitState() {//to be called at the start of the program
    snap := &StateSnapshot{
        Balances: make(map[uint64]shm.UserBalance),
        Holdings: make(map[uint64]shm.UserHoldings),
    }
    currentSnapshot.Store(snap)
}

func GetCurrentSnapshot() *StateSnapshot {
    return currentSnapshot.Load()
}






