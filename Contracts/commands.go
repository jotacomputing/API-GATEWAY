package contracts



import "github.com/gorilla/websocket"



type Command interface {
    isCommand()
}

// User subscribes to a stream
type SubscribeCommand struct {
    StreamName  string             
    Conn *websocket.Conn
}
func ( SubscribeCommand) isCommand(){}
// User unsubscribes from a stream
type UnsubscribeCommand struct {
    StreamName  string
    Conn *websocket.Conn
}
func ( UnsubscribeCommand) isCommand(){}
// Broadcast data to all subscribers of a stream
type BroadcastCommand struct {
    StreamName string 
    Data   []byte  
}
func (BroadcastCommand) isCommand(){}

type CleanupConnectionCommand struct {
    Conn *websocket.Conn
}
func ( CleanupConnectionCommand) isCommand(){}
