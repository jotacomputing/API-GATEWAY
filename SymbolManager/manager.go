package symbolmanager

import (
	"encoding/json"
	contracts "exchange/Contracts"
	"exchange/ws"
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
	Subscriber         contracts.Subscriber
	Unsubscriber       contracts.UnSubscriber
	mutex_lock         sync.RWMutex // this because their can be read write race conditions while updating the map becuase
	// it is hapoening in differnet go routines
}

func CreateSymbolManagerSingleton() *SymbolManager {
	once.Do(func() {
		SymbolManagerInstance = &SymbolManager{
			Symbol_method_subs: make(map[string][]*Client),
			Subscriber:         nil,
			Unsubscriber:       nil,
		}
	})
	return SymbolManagerInstance
}

func GetSymbolManagerInstance() *SymbolManager {
	return SymbolManagerInstance
}

func (s *SymbolManager) StartSymbolMnagaer() {
	for message := range ws.MessageChannel {
		switch message.Payload.Method {
		case "SUBSCRIBE":
			s.handleSubscribe(message)
		case "UNSUBSCRIBE":
			s.handleUnSubscribe(message)
		}
	}
}

func (s *SymbolManager) handleSubscribe(rec_mess ws.ClientMessage) {
	// if a subscribe request comes , we aqquire the read lock of the map and check if the mess.payload.symbol exists
	/// if yeh , we add to the user
	defer s.mutex_lock.Unlock()
	s.mutex_lock.Lock()

	if len(rec_mess.Payload.Params) == 0 {
		return
	}
	_, ok := s.Symbol_method_subs[rec_mess.Payload.Params[0]]

	if !ok {
		s.CreateNewGroup(rec_mess)
		// subscription can take time so spawned a go routine
		go s.Subscriber.SubscribeToSymbolMethod(rec_mess.Payload.Params[0])
		return
	}
	s.mutex_lock.Lock()
	s.Symbol_method_subs[rec_mess.Payload.Params[0]] = append(s.Symbol_method_subs[rec_mess.Payload.Params[0]], &Client{Conn: rec_mess.Socket})
	s.mutex_lock.Unlock()

}

func (s *SymbolManager) handleUnSubscribe(rec_mess ws.ClientMessage) {
	// unsubscribe messahe
	// aquire a read lock and check if it was the only user subscrbed to that event
	s.mutex_lock.Lock()
	defer s.mutex_lock.Unlock()
	clients, exists := s.Symbol_method_subs[rec_mess.Payload.Params[0]]
	if !exists {
		return // Already unsubscribed
	}

	new_clients := []*Client{}

	for _, client := range clients {
		if client.Conn != rec_mess.Socket {
			new_clients = append(new_clients, client)
		}
	}

	if len(new_clients) == 0 {
		// this was the last user , delrte the entry and unsbscribe
		delete(s.Symbol_method_subs, rec_mess.Payload.Params[0])
		if s.Unsubscriber != nil {
			s.Unsubscriber.UnSubscribeToSymbolMethod(rec_mess.Payload.Params[0])
		}

	} else {
		s.Symbol_method_subs[rec_mess.Payload.Params[0]] = new_clients
	}
}

func (s *SymbolManager) CreateNewGroup(rec_mess ws.ClientMessage) {
	// create if the groupt dosent exist for that symbol
	defer s.mutex_lock.Unlock()
	s.mutex_lock.Lock()
	clients := []*Client{}
	if rec_mess.Socket != nil {
		clients = append(clients, &Client{Conn: rec_mess.Socket})
	}
	s.Symbol_method_subs[rec_mess.Payload.Params[0]] = clients

	// the pub sub subscription is handled in the above function
}

func (s *SymbolManager) BrodCastToUsers(symbol_mothod_stream string, data []byte) {
	s.mutex_lock.RLock()
	clients := append([]*Client(nil), s.Symbol_method_subs[symbol_mothod_stream]...)
	s.mutex_lock.RUnlock()

	for _, client := range clients {
		go client.WriteMessage(websocket.TextMessage, data)
	}
}

func (s *SymbolManager) BroadCasteFromRemote(message contracts.MessageFromPubSubForUser) {
	data, _ := json.Marshal(message)
	s.BrodCastToUsers(message.Stream, data)

}


func (s *SymbolManager) CleanupConnection(conn *websocket.Conn) {
    s.mutex_lock.Lock()
    defer s.mutex_lock.Unlock()
    
    for key, clients := range s.Symbol_method_subs {
        newClients := []*Client{}
        for _, client := range clients {
            if client.Conn != conn {
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