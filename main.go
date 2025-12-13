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

	wsServer := ws.NewServer(sm.CleanupConnection)
	go wsServer.CreateServer()
	go sm.StartSymbolMnagaer()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	fmt.Println("Shutting down gracefully...")
}
