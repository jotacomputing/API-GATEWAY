package pubsubmanager

import(
	 "sync"
	 "github.com/redis/go-redis/v9"
	  "exchange/Contracts"
	  "context"
	 // "encoding/json"
	  "fmt"
	)

// the pubsusb manager exposes the subscribe , unsubscribe methods , initiates the redis pubsub clietn
var PubSubManagerInstance *PubSubManager
var once sync.Once

type  PubSubManager struct{
	rclient 		*redis.Client 
	BroadCaster 	contracts.BroadCasterForPubSub
	Subscriptions 	map[string]*redis.PubSub // keeps a track of what all streams are we subscribed to 
	mu 				sync.Mutex
}


func CreateSingletonInstance(broadcaster contracts.BroadCasterForPubSub) *PubSubManager{
	once.Do(func(){
		client := redis.NewClient(&redis.Options{
			Addr: "localhost:6379",
		})
		if err := client.Ping(context.Background()).Err(); err != nil {
			panic(err)
		}

		PubSubManagerInstance = &PubSubManager{
			rclient: client,
			BroadCaster: broadcaster,
			Subscriptions:  make(map[string]*redis.PubSub),
		}
	})

	return PubSubManagerInstance
}

func getPubSubManagerInstance() *PubSubManager{
	return PubSubManagerInstance
}


func (ps *PubSubManager)SubscribeToSymbolMethod(StreamName string){
	
	ps.mu.Lock()
	if _, already := ps.Subscriptions[StreamName]; already {
		ps.mu.Unlock()
		return
	}
	// if not subscibed
	fmt.Println("subscribing to the stream")
	pubsub := ps.rclient.Subscribe(context.Background(), StreamName)
	
	ps.Subscriptions[StreamName] = pubsub
	ps.mu.Unlock()

	
	ch := pubsub.Channel() 
	// this is the reciver go routine for every stream 
	go func() {
		for msg := range ch {
			//var m contracts.MessageFromPubSubForUser
			fmt.Println("got some  message from pubsusb",  msg)
			
			//if err := json.Unmarshal([]byte(msg.Payload), &m); err != nil {
			//	fmt.Println("Redis message unmarshal error:", err)
			//	continue
			//}
			// Notify RoomManager (via Broadcaster interface)
			ps.BroadCaster.BroadCasteFromRemote(contracts.MessageFromPubSubForUser{
				Stream: msg.Channel,
				Data:  []byte(msg.Payload),
			})
		}
	}()
}


func (ps *PubSubManager) UnSubscribeToSymbolMethod(StreamName string) {
    ps.mu.Lock()
	// take out the pubsusb obj
    pubsub, exists := ps.Subscriptions[StreamName]
    if !exists {
        ps.mu.Unlock()
        return 
    }
    delete(ps.Subscriptions, StreamName)
    ps.mu.Unlock()
	// unsubscribe methods needs to be called on the pubsub objct only 
    if err := pubsub.Unsubscribe(context.Background(), StreamName); err != nil {
        fmt.Println("Error unsubscribing:", err)
    }
	// close pubusb
    if err := pubsub.Close(); err != nil {
        fmt.Println("Error closing pubsub:", err)
    }
}

