package balances

import (
	"exchange/shm"
)



func(c *BalanceHoldingCache) Updater(){
	//listen to channels and update cache accordingly
	for {
		select{
		case br := <- BalanceUpdatesCh:
			// update balance cache
			c.mu.Lock()
			c.applyBalanceUpdate(br)
			c.mu.Unlock()

		case hr := <- HoldingUpdatesCh:
			// update holding cache
			c.mu.Lock()
			c.applyHoldingUpdate(hr)
			c.mu.Unlock()
		}
	}
}
 func addClampUint64(base uint64, delta int64) uint64 {
	if delta >= 0 {
		return base + uint64(delta)
	}
	dec := uint64(-delta)
	if dec > base {
		return 0
	}
	return base - dec
}

func addClampUint32(base uint32, delta int32) uint32 {
	if delta >= 0 {
		return base + uint32(delta)
	}
	dec := uint32(-delta)
	if dec > base {
		return 0
	}
	return base - dec
} 

func (c *BalanceHoldingCache) applyBalanceUpdate(br shm.BalanceResponse) {
	//first check if user exists
	balance, exists := c.balances[br.UserId]
	if !exists {
		// initialize new balance
		balance = shm.UserBalance{
			UserId: br.UserId,
			Available_balance: DEFAULT_BALANCE,
			Reserved_balance: 0,
			Total_traded_today: 0,
			Order_count_today: 0,
		}
	}
	// apply deltas
	balance.Available_balance = addClampUint64(balance.Available_balance, br.Delta_available_balance)
	balance.Reserved_balance = addClampUint64(balance.Reserved_balance, br.Delta_reserved_balance)
	// store back
	c.balances[br.UserId] = balance

}

func (c *BalanceHoldingCache) applyHoldingUpdate(hr shm.HoldingResponse) {
	//first check if user holdings exists
	if hr.Symbol >= MAX_SYMBOLS {
		return
	}

	holdings, exists := c.holdings[hr.UserId]
	if !exists {
		// initialize new holdings with default qty for each 10 symbols ASSIGN DEFAULT_HOLDING_QTY
		holdings = &shm.UserHoldings{
			UserId: hr.UserId,
		}
		for i := uint32(0); i < MAX_SYMBOLS; i++ {
			holdings.AvailableHoldings[i] = DEFAULT_HOLDING_QTY
		}
		// store back
		c.holdings[hr.UserId] = holdings
	}
	// apply deltas
	holdings.AvailableHoldings[hr.Symbol] = addClampUint32(holdings.AvailableHoldings[hr.Symbol], hr.Delta_available_holdings)
	holdings.ReservedHoldings[hr.Symbol] = addClampUint32(holdings.ReservedHoldings[hr.Symbol], hr.Delta_reserved_holdings)
}
