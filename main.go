package main

import (
	pubsubmanager "exchange/PubSubManager"

	symbolmanager "exchange/SymbolManager"
	"exchange/ws"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

func main() {

	sm := symbolmanager.CreateSymbolManagerSingleton()
	pubsubm := pubsubmanager.CreateSingletonInstance(sm)
	sm.Subscriber = pubsubm
	sm.Unsubscriber = pubsubm
	go sm.StartSymbolMnagaer()


	wsServer := ws.NewServer(sm , sm , sm) // passed the type 3 times 
	go wsServer.CreateServer()
	

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	fmt.Println("Shutting down gracefully...")
}
