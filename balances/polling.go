package balances

import (
	"exchange/shm"
)

func PollBalanceResponses(q *shm.BalanceResponseQueue) {
	for {
		resp, err := q.Dequeue()
		if err != nil {
			println("error polling balance responses:", err)
			continue
		}
		if resp == nil {
			continue
		}
		BalanceUpdatesCh <- *resp
	}
}

func PollHoldingResponses(q *shm.HoldingResponseQueue) {
	for {
		resp, err := q.Dequeue()
		if err != nil {
			println("error polling holding responses:", err)
			continue
		}
		if resp == nil {
			continue
		}
		HoldingUpdatesCh <- *resp
	}
}
