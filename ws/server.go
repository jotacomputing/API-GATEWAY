package ws

import (
	"encoding/json"
	contracts "exchange/Contracts"
	symbolmanager "exchange/SymbolManager"
	"fmt"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
)

var upgrader = websocket.Upgrader{}

type ClientMessage struct {
	Socket  *websocket.Conn // connection objexct needs to be sent along with the message
	Payload contracts.MessageFromUser
}
type Server struct {
	// functions from symbol manager that just pass the commands into the channel
	// no need of the interface
	symbol_manager_ptr *symbolmanager.SymbolManager
}

func NewServer(
	symbo_manager_ptr *symbolmanager.SymbolManager,
) *Server {
	return &Server{
		symbol_manager_ptr: symbo_manager_ptr,
	}
}

func (s *Server) wsHandler(c echo.Context) error {

	ws, err := upgrader.Upgrade(c.Response(), c.Request(), nil)

	if err != nil {
		fmt.Println("UPGRADE ERROR:", err)
		return err
	}
	defer func() {
		ws.Close()
		s.symbol_manager_ptr.CleanupConnection(ws)
	}()

	var mess contracts.MessageFromUser
	//fmt.Println("WebSocket connection established!")

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
		fmt.Println("Recived message")
		switch mess.Method {
		case contracts.SUBSCRIBE:
			if len(mess.Params) > 0 {
				s.symbol_manager_ptr.Subscribe(mess.Params[0], ws)
			}

		case contracts.UNSUBSCRIBE:
			if len(mess.Params) > 0 {
				s.symbol_manager_ptr.UnSubscribe(mess.Params[0], ws)
			}
		}

	}
}

func (s *Server) CreateServer() {
	fmt.Println("BOOTING SERVER...")

	e := echo.New()
	e.GET("/ws", s.wsHandler)

	fmt.Println("LISTENING on :8080 ...")

	err := e.Start(":8080")
	fmt.Println("SERVER EXITED:", err)
}
