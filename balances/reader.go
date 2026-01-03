package balances

import (
	"context"
	"exchange/shm"
)

type GetBalanceReq struct {
	UserID  uint64
	ReplyCh chan shm.UserBalance // buffered(1)
}

type GetHoldingsReq struct {
	UserID  uint64
	ReplyCh chan shm.UserHoldings //copied buffered(1)
}

type HoldingItem struct {
	Symbol    uint32 `json:"symbol"`
	Available uint32 `json:"available"`
	Reserved  uint32 `json:"reserved"`
}

type HoldingsReply struct {
	UserId   uint64        `json:"user_id"`
	Holdings []HoldingItem `json:"holdings"`
}

func (c *BalanceHoldingCache) RunReader(
	ctx context.Context,
	getBalCh <-chan GetBalanceReq,
	getHoldCh <-chan GetHoldingsReq,
) {
	for getBalCh != nil || getHoldCh != nil {
		select {
		case <-ctx.Done():
			return

		case req, ok := <-getBalCh:
			if !ok {
				getBalCh = nil
				continue
			}

			c.mu.Lock()
			bal, ok2 := c.balances[req.UserID]
			if !ok2 {
				bal = shm.UserBalance{UserId: req.UserID}
			}
			c.mu.Unlock()

			// Reply channel is buffered(1) so this send won't block in normal cases. [web:209]
			req.ReplyCh <- bal

		case req, ok := <-getHoldCh:
			if !ok {
				getHoldCh = nil
				continue
			}

			c.mu.Lock()

			uh := c.holdings[req.UserID]
			var out shm.UserHoldings
			if uh == nil {
				out = shm.UserHoldings{UserId: req.UserID}
			} else {
				out = *uh // copy
			}
			req.ReplyCh <- out
			c.mu.Unlock()
		}
	}
}
