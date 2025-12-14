package contracts

import "github.com/gorilla/websocket"


// For SymbolManager to call PubSubManager so subscriptons to pub sub can be managed 
type SubscriberToPubSub interface{
	SubscribeToSymbolMethod(StreamName string)
}
type UnSubscriberToPubSub interface {
	UnSubscribeToSymbolMethod(StreamName string)
}

// for pubsusb manager to call of symbol manager 
type BroadCasterForPubSub interface {
	BroadCasteFromRemote(mess MessageFromPubSubForUser)
}


// THE WS NEEDS TO CALL SM.SUBSCRIBE SM.UNSUBSCRIBE SM.CLEANUP 
type SubscriberForWs interface{
	Subscribe(StreamName string , conn *websocket.Conn)
}

type UnSubscriberForWs interface{
	UnSubscribe(StreamName string , conn *websocket.Conn)
}

type CleanUpForWs interface{
	CleanupConnection(conn *websocket.Conn)
}

