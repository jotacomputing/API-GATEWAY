package hub

import (
	"encoding/json"
	"exchange/shm"
	"fmt"

	"github.com/gorilla/websocket"
)

type ClientInterface interface{
	GetUserId()  uint64
	GetConnObj() *websocket.Conn
	GetSendCh()	 chan []byte
}
// for any type to be a client interface it must implement these functions , so i made client implement these functions this we can use client freely as a ClientInterface 
type OrderEventsHub struct {
	connections map[uint64][]ClientInterface 
	registerChan    chan ClientInterface
    unregisterChan  chan ClientInterface
    broadcastChan   chan shm.OrderEvent
}


func NewOrderEventHub()*OrderEventsHub{
	return &OrderEventsHub{
		connections: make(map[uint64][]ClientInterface),
		registerChan: make(chan ClientInterface , 256),
		unregisterChan: make(chan ClientInterface , 256),
		broadcastChan: make(chan shm.OrderEvent , 10000),
	}
}
// need to expose registern , unregister functions for the server pointer to call them in the hadnler 
func (oh *OrderEventsHub)Register(client_type ClientInterface){
	oh.registerChan <- client_type
}
func (oh *OrderEventsHub)UnRegister(client_type ClientInterface){
	oh.unregisterChan <- client_type
}


func(oh*OrderEventsHub)Start(){
	for {
		select{
			case client := <-oh.registerChan:
				user_id := client.GetUserId()
				oh.connections[user_id] = append(oh.connections[user_id], client)
			case client := <-oh.unregisterChan:
				user_id := client.GetUserId()
				if clients , ok := oh.connections[user_id]; ok{
					// exists 
					new_clients := make([]ClientInterface , 0 , len(clients)-1)

					for _ , connobj := range clients{
						if connobj != client{
							new_clients = append(new_clients, connobj)
						}
					}

					if len(new_clients) == 0 {
						delete(oh.connections, user_id)
					}else{
						oh.connections[user_id] = new_clients
					}

				}

			case event := <-oh.broadcastChan:
				bytes , err := json.Marshal(event)
				if err != nil{
					fmt.Println("marshal error ")
					return 
				}
				clients := oh.connections[event.UserId]

            		for _, client := range clients {
            		    
            		     client.GetSendCh() <- bytes 
            		    
            		}
				
		}
	}
}