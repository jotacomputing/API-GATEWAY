package shm

import (
	"fmt"
)

type BrodCaster interface {
	BrodCast(event OrderEvent)
}

type ShmManager struct {
	Balance_Response_queue *BalanceResponseQueue
	CancelOrderQueue       *CancelOrderQueue
	Holding_Response_queue *HoldingResponseQueue
	Order_Events_queue     *OrderEventQueue
	Post_Order_queue       *Queue
	Query_queue            *QueryQueue
	Incoming_MM_queue     *MMQueue
	MM_Response_queue     *MMResponseQueue
	BrodCaster             BrodCaster
}

func GetShmManager(Balance_Response_queue *BalanceResponseQueue,
	CancelOrderQueue *CancelOrderQueue,
	Holding_Response_queue *HoldingResponseQueue,
	Order_Evenets_queue *OrderEventQueue,
	Post_Order_queue *Queue,
	Query_queue *QueryQueue,
	Incoming_MM_queue *MMQueue,
	MM_Response_queue *MMResponseQueue,
) *ShmManager {
	return &ShmManager{
		Balance_Response_queue: Balance_Response_queue,
		CancelOrderQueue:       CancelOrderQueue,
		Holding_Response_queue: Holding_Response_queue,
		Order_Events_queue:     Order_Evenets_queue,
		Post_Order_queue:       Post_Order_queue,
		Query_queue:            Query_queue,
		Incoming_MM_queue:     Incoming_MM_queue,
		MM_Response_queue:     MM_Response_queue,
	}
}

// function to launch go routines to poll the order events and the query response queue

func (m *ShmManager) PollOrderEvents(out chan<- OrderEvent) {
	fmt.Println("startigng poller")
	for {

		event, err := m.Order_Events_queue.Dequeue()

		if err != nil {
			fmt.Println("error dequeuing order event:", err)
		}
		if event == nil {
			continue
		}
		fmt.Println(event)
		// push the event to the out channel
		out <- *event

	}
}


