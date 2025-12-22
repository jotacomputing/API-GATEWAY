package contracts



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


