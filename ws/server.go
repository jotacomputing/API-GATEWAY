package ws

import (
	"fmt"
	"encoding/json"
	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
	"exchange/contracts"
)

var upgrader = websocket.Upgrader{}

type ClientMessage struct {
	Socket *websocket.Conn // connection objexct needs to be sent along with the message 
	Payload contracts.Message
}

var MessageChannel = make(chan ClientMessage , 100)
func wsHandler(c echo.Context) error {

	ws, err := upgrader.Upgrade(c.Response(), c.Request(), nil)

	if err != nil {
		fmt.Println("UPGRADE ERROR:", err)
		return err
	}
	defer ws.Close()
	var mess contracts.Message ;
	fmt.Println("WebSocket connection established!")

	for {
		_, p, err := ws.ReadMessage()
		if err != nil {
			fmt.Println("READ ERROR:", err)
			return nil
		}
		if err := json.Unmarshal(p, &mess); err != nil {
			fmt.Println("json error:", err)
			continue
		}
		fmt.Println("Recived message");
		fmt.Println(mess)
		MessageChannel <- ClientMessage{Socket: ws , Payload: mess}
		
	}
}

func CreateServer() {
	fmt.Println("BOOTING SERVER...")

	e := echo.New()
	e.GET("/ws", wsHandler)

	fmt.Println("LISTENING on :8080 ...")

	err := e.Start(":8080")
	fmt.Println("SERVER EXITED:", err)
}
