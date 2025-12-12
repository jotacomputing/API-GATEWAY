package contracts

import (
	
)
type Method string
const (
	SUBSCRIBE 	Method = "SUBSCRIBE"
	UNSUBSCRIBE Method = "UNSUBSCRIBE"
)

type Message struct {
	Method Method `json:"method"`
    Params []string `json:"params"`
    ID     int      `json:"id"`
}


type  Publisher interface{
	PublishToMembers(channelName string , mess []byte)error
}

type Subscriber interface{
	SubscribeToSymbolMethod(channelName string)
}