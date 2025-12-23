package main

import (
	pubsubmanager "exchange/PubSubManager"
	symbolmanager "exchange/SymbolManager"
	ws "exchange/Ws"
	shm "exchange/Shm"
	hub "exchange/Hub"
	"fmt"
	"os"
	"os/signal"
	"syscall"

)

func main() {
	balance_Response_queue , berr := shm.OpenBalanceResponseQueue("/tmp/trading/BalanceResponse")
	if berr!=nil{
		return 
	}
	cancel_order_queue , cerr := shm.OpenCancelOrderQueue("/tmp/trading/CancelOrders")
	if cerr!=nil{
		return
	}
	holdings_response_queue , herr := shm.OpenHoldingResponseQueue("/tmp/trading/HoldingsResponse")
	if herr!=nil{
		return 
	}
	order_events_queue , oerr := shm.OpenOrderEventQueue("/tmp/trading/OrderEvents")
	if oerr!=nil{
		return 
	}
	post_order_queue , qerr := shm.OpenQueue("/tmp/trading/IncomingOrdersForMe")
	if qerr!=nil{
		return
	}
	queries_queue , querr := shm.OpenQueryQueue("/tmp/trading/Queries")
	if querr!=nil{
		return 
	}

	sm := symbolmanager.CreateSymbolManagerSingleton()
	pubsubm := pubsubmanager.CreateSingletonInstance(sm)
	sm.Subscriber = pubsubm
	sm.Unsubscriber = pubsubm
	go sm.StartSymbolMnagaer()


	order_event_hub := hub.NewOrderEventHub()
	go order_event_hub.Start()
	wsServer := ws.NewServer(sm , order_event_hub)
	go wsServer.CreateServer()
	
	
	

	shmmanager:= shm.ShmManager{
		Balance_Response_queue: balance_Response_queue,
		CancelOrderQueue: cancel_order_queue,
		Holding_Response_queue: holdings_response_queue,
		Order_Events_queue: order_events_queue,
		Post_Order_queue: post_order_queue,
		Query_queue: queries_queue,
	}
	shmmanager.BrodCaster = order_event_hub
	go shmmanager.PollOrderEvents()


	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan
	fmt.Println("Shutting down gracefully...")
}
