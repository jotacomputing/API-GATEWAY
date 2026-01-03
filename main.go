package main

import (
	pubsubmanager "exchange/PubSubManager"
	symbolmanager "exchange/SymbolManager"
	ws "exchange/ws"
	shm "exchange/shm"
	hub "exchange/Hub"
	balances "exchange/balances"
	"fmt"
	"os"
	"os/signal"
	"syscall"

)

func main() {
	balance_Response_queue , berr := shm.OpenBalanceResponseQueue("/tmp/BalanceResponse")
	if berr!=nil{
		panic(fmt.Errorf("BalanceResponseQueue error: %w", berr))
	}
	cancel_order_queue , cerr := shm.OpenCancelOrderQueue("/tmp/CancelOrders")
	if cerr!=nil{
		panic(fmt.Errorf("OpenCancelOrderQueue error: %w", berr))
	}
	holdings_response_queue , herr := shm.OpenHoldingResponseQueue("/tmp/HoldingsResponse")
	if herr!=nil{
		panic(fmt.Errorf("OpenHoldingResponseQueue error: %w", berr))
	}
	order_events_queue , oerr := shm.OpenOrderEventQueue("/tmp/OrderEvents")
	if oerr!=nil{
		panic(fmt.Errorf("OpenOrderEventQueue error: %w", berr))
	}
	post_order_queue , qerr := shm.OpenQueue("/tmp/IncomingOrders")
	if qerr!=nil{
		panic(fmt.Errorf("OpenQueue error: %w", berr))
	}
	queries_queue , querr := shm.OpenQueryQueue("/tmp/Queries")
	if querr!=nil{
		panic(fmt.Errorf("OpenQueryQueue error: %w", berr))
	}

	sm := symbolmanager.CreateSymbolManagerSingleton()
	pubsubm := pubsubmanager.CreateSingletonInstance(sm)
	sm.Subscriber = pubsubm
	sm.Unsubscriber = pubsubm
	go sm.StartSymbolMnagaer()

	shmmanager:= shm.ShmManager{
		Balance_Response_queue: balance_Response_queue,
		CancelOrderQueue: cancel_order_queue,
		Holding_Response_queue: holdings_response_queue,
		Order_Events_queue: order_events_queue,
		Post_Order_queue: post_order_queue,
		Query_queue: queries_queue,
	}

	order_event_hub := hub.NewOrderEventHub()
	go order_event_hub.Start()

	cache := balances.NewBalanceHoldingCache()

	wsServer := ws.NewServer(sm , order_event_hub, &shmmanager,cache)	
	go wsServer.CreateServer()

	
	shmmanager.BrodCaster = order_event_hub
	go shmmanager.PollOrderEvents()


	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan
	fmt.Println("Shutting down gracefully...")
}
