package main

import (
	pubsubmanager "exchange/PubSubManager"
	symbolmanager "exchange/SymbolManager"
	ws "exchange/Ws"
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

	wsServer := ws.NewServer(sm)
	// do we need go rotine here ?? idts the server can run in the main routine
	go wsServer.CreateServer()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan
	fmt.Println("Shutting down gracefully...")
}
