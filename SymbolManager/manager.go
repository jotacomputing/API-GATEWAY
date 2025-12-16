package symbolmanager

import (
	"encoding/json"
	contracts "exchange/Contracts"
	"fmt"
	"sync"

	"github.com/gorilla/websocket"
)

// receives the message from the web socket go routine lanched oer client , message can be of two typs subscribe and unsubscribe
// a singelton patteern of the symbol manager

var SymbolManagerInstance *SymbolManager
var once sync.Once

type Client struct {
	Conn      *websocket.Conn
	writeLock sync.Mutex
}

func (c *Client) WriteMessage(messageType int, data []byte) error {
	c.writeLock.Lock()
	defer c.writeLock.Unlock()
	return c.Conn.WriteMessage(messageType, data)
}

type SymbolManager struct {
	Symbol_method_subs map[string][]*Client // keeps a track of the different streams and the subscirbed clients
	Subscriber         contracts.SubscriberToPubSub
	Unsubscriber       contracts.UnSubscriberToPubSub
	CommandChan        chan contracts.Command
}

func CreateSymbolManagerSingleton() *SymbolManager {
	once.Do(func() {
		SymbolManagerInstance = &SymbolManager{
			Symbol_method_subs: make(map[string][]*Client),
			Subscriber:         nil,
			Unsubscriber:       nil,
			CommandChan:        make(chan contracts.Command, 1000),
		}
	})
	return SymbolManagerInstance
}

func GetSymbolManagerInstance() *SymbolManager {
	return SymbolManagerInstance
}

// methofs for ws handler
func (sm *SymbolManager) Subscribe(StreamName string, conn *websocket.Conn) {
	fmt.Println("passing command to channel")
	sm.CommandChan <- contracts.SubscribeCommand{
		StreamName: StreamName,
		Conn:       conn,
	}
}

func (sm *SymbolManager) UnSubscribe(StreamName string, conn *websocket.Conn) {
	sm.CommandChan <- contracts.UnsubscribeCommand{
		StreamName: StreamName,
		Conn:       conn,
	}
}

func (sm *SymbolManager) CleanupConnection(conn *websocket.Conn) {
	sm.CommandChan <- contracts.CleanupConnectionCommand{
		Conn: conn,
	}
}

// for the pubsusb manager
func (sm *SymbolManager) BroadCasteFromRemote(message contracts.MessageFromPubSubForUser) {
	fmt.Println("received brodcast request sedning to channel ")
	data, _ := json.Marshal(message)
	sm.CommandChan <- contracts.BroadcastCommand{
		StreamName: message.Stream,
		Data:       data,
	}
}

func (sm *SymbolManager) StartSymbolMnagaer() {
	for command := range sm.CommandChan {
		fmt.Println("sybol manager got the command")
		fmt.Println(command)
		switch c := command.(type) {
		case contracts.SubscribeCommand:
			fmt.Println("sybol manager got the subsirbe command")
			sm.handleSubscribeInternal(c)

		case contracts.UnsubscribeCommand:
			sm.handleUnsubscribeInternal(c)

		case contracts.CleanupConnectionCommand:
			sm.handleCleanupInternal(c)

		case contracts.BroadcastCommand:
			fmt.Println("sybol manager got the brodacast command")
			sm.handleBroadcastInternal(c)

		}
	}
}

// internal subscribe amd unsbbsrcibe methods , that will be called when we recive commands from the channel

func (sm *SymbolManager) handleSubscribeInternal(cmd contracts.SubscribeCommand) {

	clients, exists := sm.Symbol_method_subs[cmd.StreamName]

	if !exists {
		fmt.Println("initilising stream key in map calling creategrp")
		// First subscriber
		sm.Symbol_method_subs[cmd.StreamName] = []*Client{{Conn: cmd.Conn}}
		// subscription can take time so spawned a go routine
		fmt.Println("sbscrbing to pubsubs")
		go sm.Subscriber.SubscribeToSymbolMethod(cmd.StreamName)
		return
	} else {
		sm.Symbol_method_subs[cmd.StreamName] = append(clients, &Client{Conn: cmd.Conn})
	}

}

func (sm *SymbolManager) handleUnsubscribeInternal(cmd contracts.UnsubscribeCommand) {

	clients, exists := sm.Symbol_method_subs[cmd.StreamName]
	if !exists {
		return // Already unsubscribed
	}

	new_clients := []*Client{}

	for _, client := range clients {
		if client.Conn != cmd.Conn {
			new_clients = append(new_clients, client)
		}
	}

	if len(new_clients) == 0 {
		// this was the last user , delrte the entry and unsbscribe
		delete(sm.Symbol_method_subs, cmd.StreamName)
		if sm.Unsubscriber != nil {
			go sm.Unsubscriber.UnSubscribeToSymbolMethod(cmd.StreamName)
		}

	} else {
		sm.Symbol_method_subs[cmd.StreamName] = new_clients
	}
}

func (sm *SymbolManager) handleBroadcastInternal(cmd contracts.BroadcastCommand) {
	fmt.Println("inside internal brodacst ")
	fmt.Println(sm.Symbol_method_subs)
	
    for _, client := range sm.Symbol_method_subs[cmd.StreamName] {
		fmt.Println(client)
		// important decision to write go or not here
        go client.WriteMessage(websocket.TextMessage, cmd.Data)
    }
}

func (s *SymbolManager) handleCleanupInternal(cmd contracts.CleanupConnectionCommand) {

	for key, clients := range s.Symbol_method_subs {
		newClients := []*Client{}
		for _, client := range clients {
			if client.Conn != cmd.Conn {
				newClients = append(newClients, client)
			}
		}

		if len(newClients) == 0 {
			delete(s.Symbol_method_subs, key)
			if s.Unsubscriber != nil {
				go s.Unsubscriber.UnSubscribeToSymbolMethod(key)
			}
		} else if len(newClients) != len(clients) {
			s.Symbol_method_subs[key] = newClients
		}
	}
}
