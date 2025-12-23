package ws

import (
	"encoding/json"
	contracts "exchange/Contracts"
	hub "exchange/Hub"
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
	order_events_hub_ptr 	*hub.OrderEventsHub
}

func NewServer(
	symbo_manager_ptr *symbolmanager.SymbolManager,
	order_events_hub_ptr 	*hub.OrderEventsHub, // for subscirbing unsibsicribing 
) *Server {
	return &Server{
		symbol_manager_ptr: symbo_manager_ptr,
		order_events_hub_ptr: order_events_hub_ptr,
	}
}

func (s *Server) wsHandlerMd(c echo.Context) error {

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

type ClientForOrderEvents struct {
	UserId 	uint64
	Conn 	*websocket.Conn
	SendCh	chan []byte
}

// interface functions for hub 
func (cl *ClientForOrderEvents)GetUserId()uint64{
	return cl.UserId
}
func (cl *ClientForOrderEvents)GetConnObj()*websocket.Conn{
	return cl.Conn
}
func (cl *ClientForOrderEvents)GetSendCh()chan []byte{
	return cl.SendCh
}



func (coe *ClientForOrderEvents) WritePumpForOrderEv() {
    for {

        message, ok := <-coe.SendCh  
		fmt.Println("wrtie routine got message")
		fmt.Println(string(message))
        if !ok {
           // chnnel closed
            return
        }
        if err := coe.Conn.WriteMessage(websocket.BinaryMessage, message); err != nil {
            return
        }
    }
}


func (s*Server)wsHandlerOrderEvents(c echo.Context)error{
	// authenticate thishandler , give me the exracted userId 
	fmt.Println("inside handler ")
	user_id := uint64(20) // give this from auth 
	fmt.Println(user_id)
	conn , err := upgrader.Upgrade(c.Response() , c.Request() , nil)
	if err!=nil{
		fmt.Println("error upgrading connection")
		return err
	}

	client := &ClientForOrderEvents{
		UserId: user_id,
		Conn: conn,
		SendCh: make(chan []byte , 256),
	}
	s.order_events_hub_ptr.Register(client)
	go client.WritePumpForOrderEv()
	defer func(){
		
		s.order_events_hub_ptr.UnRegister(client)
		conn.Close()
		// or
		//
		//client.hub_ptr.UnRegister(conn)
	}()
	for {
		_ , _ , err:= client.Conn.ReadMessage()
		if err!=nil{
			fmt.Println("read error")
			return nil
		}
	}
	// read routine dosent do anyhitng 

	
}

func (s *Server) CreateServer() {
	fmt.Println("BOOTING SERVER...")

	e := echo.New()
	e.GET("/ws/marketData", s.wsHandlerMd)
	e.GET("/ws/OrderEvents", s.wsHandlerOrderEvents)

	fmt.Println("LISTENING on :8080 ...")

	err := e.Start(":8080")
	fmt.Println("SERVER EXITED:", err)
}
