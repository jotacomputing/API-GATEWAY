package balances
import (
	"exchange/shm"
	"sync"
)

const MAX_SYMBOLS = 10
const DEFAULT_BALANCE = 100000000
const DEFAULT_HOLDING_QTY = 10000

type BalanceHoldingCache struct {
	mu       sync.Mutex
	balances map[uint64]shm.UserBalance
	holdings map[uint64]*shm.UserHoldings
}

var (
	BalanceUpdatesCh = make(chan shm.BalanceResponse, 1024)
	HoldingUpdatesCh = make(chan shm.HoldingResponse, 1024)
)


func NewBalanceHoldingCache() *BalanceHoldingCache {
	return &BalanceHoldingCache{
		balances: make(map[uint64]shm.UserBalance, 1<<16),
		holdings: make(map[uint64]*shm.UserHoldings, 1<<16),
	}
}






