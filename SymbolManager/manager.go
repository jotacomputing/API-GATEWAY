package symbolmanager

import (
	"exchange/Contracts"
	
	"sync"

	"github.com/gorilla/websocket"
)

// receives the message from the web socket go routine lanched oer client , message can be of two typs subscribe and unsubscribe
// a singelton patteern of the symbol manager

var SymbolManagerInstance *SymbolManager 
var once sync.Once
type Client struct{
	ws_conn *websocket.Conn
}
type SymbolManager struct{
	symbol_method_subs map[string][]*Client
	publisher contracts.Publisher 
	subscriber contracts.Subscriber
}