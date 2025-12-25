package balances

import (
	"time"
	"exchange/shm"
)

func PollBalanceResponses(q *shm.BalanceResponseQueue) {
	for {
		resp, err := q.Dequeue()
		if err != nil {
			// log error, maybe sleep/backoff
			time.Sleep(50 * time.Millisecond)
			continue
		}
		if resp == nil {
			// empty queue – small sleep to avoid busy spin
			time.Sleep(50 * time.Microsecond)
			continue
		}
		BalanceUpdatesCh <- *resp
	}
}

func PollHoldingResponses(q *shm.HoldingResponseQueue) {
	for {
		resp, err := q.Dequeue()
		if err != nil {
			// log error, maybe sleep/backoff
			time.Sleep(50 * time.Millisecond)
			continue
		}
		if resp == nil {
			time.Sleep(50 * time.Microsecond)
			continue
		}
		HoldingUpdatesCh <- *resp
	}
}
