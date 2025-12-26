package ws

import (
	"encoding/json"
	contracts "exchange/Contracts"
	hub "exchange/Hub"
	symbolmanager "exchange/SymbolManager"
	"exchange/db"
	"fmt"

	"github.com/gorilla/websocket"

	"context"
	"database/sql"
	"exchange/shm"
	"net/http"
	"strconv"
	"exchange/balances"
	echoserver "github.com/dasjott/oauth2-echo-server"
	"github.com/go-oauth2/oauth2/v4"
	"github.com/go-oauth2/oauth2/v4/manage"
	"github.com/go-oauth2/oauth2/v4/models"
	"github.com/go-oauth2/oauth2/v4/server"
	"github.com/go-oauth2/oauth2/v4/store"
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
	symbol_manager_ptr   *symbolmanager.SymbolManager
	order_events_hub_ptr *hub.OrderEventsHub
	shm_manager_ptr      *shm.ShmManager
}

func NewServer(
	symbo_manager_ptr *symbolmanager.SymbolManager,
	order_events_hub_ptr *hub.OrderEventsHub, // for subscirbing unsibsicribing
	shm_manager_ptr *shm.ShmManager,
) *Server {
	return &Server{
		symbol_manager_ptr:   symbo_manager_ptr,
		order_events_hub_ptr: order_events_hub_ptr,
		shm_manager_ptr:      shm_manager_ptr,
	}
}

func (s *Server) GetBalanceHandler(c echo.Context) error {
    ti, exists := c.Get(echoserver.DefaultConfig.TokenKey).(oauth2.TokenInfo)
	if !exists {
		return echo.NewHTTPError(http.StatusUnauthorized, "Invalid or missing token")
	}

	// Parse string userID back to uint64 (matches your matching engine)
	userIDStr := ti.GetUserID()
	userID, err := strconv.ParseUint(userIDStr, 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Invalid user ID format")
	} 

    snap := balances.GetCurrentSnapshot()
    bal, ok := snap.Balances[userID]
    if !ok {
        bal = shm.UserBalance{UserId: userID} // default zero balance
    }

    return c.JSON(http.StatusOK, bal)
}

func (s *Server) GetHoldingsHandler(c echo.Context) error {
    ti, exists := c.Get(echoserver.DefaultConfig.TokenKey).(oauth2.TokenInfo)
	if !exists {
		return echo.NewHTTPError(http.StatusUnauthorized, "Invalid or missing token")
	}

	// Parse string userID back to uint64 (matches your matching engine)
	userIDStr := ti.GetUserID()
	userID, err := strconv.ParseUint(userIDStr, 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Invalid user ID format")
	} 

    snap := balances.GetCurrentSnapshot()
    h, ok := snap.Holdings[userID]
    if !ok {
        h = shm.UserHoldings{UserId: userID} // default empty holdings
    }

    return c.JSON(http.StatusOK, h)
}

func (s *Server) CancelOrderHandler(c echo.Context) error {
	// Get authenticated user from OAuth2 token
	ti, exists := c.Get(echoserver.DefaultConfig.TokenKey).(oauth2.TokenInfo)
	if !exists {
		return echo.NewHTTPError(http.StatusUnauthorized, "Invalid or missing token")
	}

	// Parse string userID back to uint64 (matches your matching engine)
	userIDStr := ti.GetUserID()
	userID, err := strconv.ParseUint(userIDStr, 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Invalid user ID format")
	}

	// Get orderId from URL param
	/* orderIDStr := c.Param("orderId")
	orderID, err := strconv.ParseUint(orderIDStr, 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid order ID format")
	} */
	
	var tempOrderToBeCanceled shm.TempOrderToBeCanceled
	if err := c.Bind(&tempOrderToBeCanceled); err != nil {
		return c.JSON(400, map[string]string{"error": "Invalid request body"})
	}

	var cancelOrder shm.OrderToBeCanceled
	cancelOrder.OrderId = tempOrderToBeCanceled.OrderId
	cancelOrder.Symbol = tempOrderToBeCanceled.Symbol
	cancelOrder.UserId = userID

	// Enqueue the cancel order request
	if err := s.shm_manager_ptr.CancelOrderQueue.Enqueue(cancelOrder); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to enqueue cancel order request")
	}

	return c.JSON(http.StatusOK, map[string]interface{}{
		"status":   "Cancel order request placed successfully",
		"order_id": cancelOrder.OrderId,
		"user_id":  cancelOrder.UserId,
	})
}



func (s *Server) PostOrderHandler(c echo.Context) error {
	// Get authenticated user from OAuth2 token
	ti, exists := c.Get(echoserver.DefaultConfig.TokenKey).(oauth2.TokenInfo)
	if !exists {
		return echo.NewHTTPError(http.StatusUnauthorized, "Invalid or missing token")
	}

	// Parse string userID back to uint64 (matches your matching engine)
	userIDStr := ti.GetUserID()
	userID, err := strconv.ParseUint(userIDStr, 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Invalid user ID format")
	} 

	var tempOrder shm.TempOrder
	if err := c.Bind(&tempOrder); err != nil {
		return c.JSON(400, map[string]string{"error": "Invalid request body"})
	}

	// Validate order fields
	if err := tempOrder.Validate(); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	// Create order with AUTHENTICATED user_id (secure - from token, not request!)
	var order shm.Order
	order.OrderID = tempOrder.OrderID
	order.Price = tempOrder.Price
	order.Timestamp = tempOrder.Timestamp
	order.User_id = userID
	order.Quantity = tempOrder.Quantity
	order.Symbol = tempOrder.Symbol
	order.Side = tempOrder.Side
	order.Order_type = tempOrder.Order_type
	order.Status = 0 // pending

	// Enqueue the order
	if err := s.shm_manager_ptr.Post_Order_queue.Enqueue(order); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to enqueue order")
	}

	return c.JSON(http.StatusOK, map[string]interface{}{
		"status":   "Order placed successfully",
		"order_id": order.OrderID,
		"user_id":  order.User_id,
		"symbol":   order.Symbol,
	})
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
	UserId uint64
	Conn   *websocket.Conn
	SendCh chan []byte
}

// interface functions for hub
func (cl *ClientForOrderEvents) GetUserId() uint64 {
	return cl.UserId
}
func (cl *ClientForOrderEvents) GetConnObj() *websocket.Conn {
	return cl.Conn
}
func (cl *ClientForOrderEvents) GetSendCh() chan []byte {
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

func (s *Server) wsHandlerOrderEvents(c echo.Context) error {
	// authenticate thishandler , give me the exracted userId
	 ti, exists := c.Get(echoserver.DefaultConfig.TokenKey).(oauth2.TokenInfo)
	if !exists {
		return echo.NewHTTPError(http.StatusUnauthorized, "Invalid or missing token")
	}

	// Parse string userID back to uint64 (matches your matching engine)
	userIDStr := ti.GetUserID()
	userID, err := strconv.ParseUint(userIDStr, 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Invalid user ID format")
	}
 
	fmt.Println("inside handler ")
	user_id := userID // give this from auth
	fmt.Println(user_id)
	conn, err := upgrader.Upgrade(c.Response(), c.Request(), nil)
	if err != nil {
		fmt.Println("error upgrading connection")
		return err
	}

	client := &ClientForOrderEvents{
		UserId: user_id,
		Conn:   conn,
		SendCh: make(chan []byte, 256),
	}
	s.order_events_hub_ptr.Register(client)
	go client.WritePumpForOrderEv()
	defer func() {

		s.order_events_hub_ptr.UnRegister(client)
		conn.Close()
		// or
		//
		//client.hub_ptr.UnRegister(conn)
	}()
	for {
		_, _, err := client.Conn.ReadMessage()
		if err != nil {
			fmt.Println("read error")
			return nil
		}
	}
	// read routine dosent do anyhitng

}

func (s *Server) CreateServer() {
	fmt.Println("BOOTING SERVER...")

	//init db
	db.InitDB()

	balances.InitState()

    go balances.PollBalanceResponses(s.shm_manager_ptr.Balance_Response_queue)
    go balances.PollHoldingResponses(s.shm_manager_ptr.Holding_Response_queue)
    go balances.StateUpdater()
	manager := manage.NewDefaultManager()
	manager.MustTokenStorage(store.NewFileTokenStore("data.db"))

	// Register OAuth2 client
	clientStore := store.NewClientStore()
	clientStore.Set("stock-app", &models.Client{
		ID:     "stock-app",
		Secret: "supersecret",
		Domain: "http://localhost:1323",
	})
	manager.MapClientStorage(clientStore)

	// Init Echo OAuth2 server
	echoserver.InitServer(manager)
	echoserver.SetAllowGetAccessRequest(true)
	echoserver.SetClientInfoHandler(server.ClientFormHandler)

	// 1) Allow PASSWORD grant
	echoserver.SetAllowedGrantType(oauth2.PasswordCredentials)

	// 2) Optional: ensure this client may use password grant
	echoserver.SetClientAuthorizedHandler(func(clientID string, grant oauth2.GrantType) (bool, error) {
		if clientID == "stock-app" && grant == oauth2.PasswordCredentials {
			return true, nil
		}
		return false, nil
	})

	// 3) PasswordAuthorizationHandler: check username/password, return user ID
	echoserver.SetPasswordAuthorizationHandler(
		func(ctx context.Context, clientID, username, password string) (string, error) {
			// For testing: accept any non-empty username/password
			if username == "" || password == "" {
				return "", echo.NewHTTPError(http.StatusUnauthorized, "invalid credentials")
			}

			// Optionally verify clientID
			if clientID != "stock-app" {
				return "", echo.NewHTTPError(http.StatusUnauthorized, "invalid client")
			}

			// Find or create user in database
			user, err := db.FindUserByUsername(username)
			if err == sql.ErrNoRows {
				// New user → create with default balance 0.0
				user, err = db.CreateUser(username, 0.0)
				if err != nil {
					return "", err
				}

				//write a query to add user to balance manager

				var query shm.Query
				query.QueryId = 0   //deafult for new logins
				query.QueryType = 2 // add user on login
				query.UserId = uint64(user.ID)
				// Enqueue the query
				if s.shm_manager_ptr.Query_queue != nil {
					s.shm_manager_ptr.Query_queue.Enqueue(query)
				} else {
					fmt.Println("Warning: QueriesQueue is nil")
				}
			}
			if err != nil {
				return "", err
			}
			return strconv.FormatUint(user.ID, 10), nil
		},
	)

	e := echo.New()

	// OAuth2 endpoint
	oauth := e.Group("/oauth2")
	oauth.POST("/token", echoserver.HandleTokenRequest)

	//api endpoints
	api := e.Group("/api")
	api.Use(echoserver.TokenHandler())

	api.POST("/order", s.PostOrderHandler)
	api.GET("/balance", s.GetBalanceHandler)
	api.GET("/holdings", s.GetHoldingsHandler)
	api.DELETE("/cancel", s.CancelOrderHandler) 

	ws := e.Group("/ws")
	ws.Use(echoserver.TokenHandler())

	ws.GET("/marketData", s.wsHandlerMd)
	ws.GET("/OrderEvents", s.wsHandlerOrderEvents)

	fmt.Println("LISTENING on :8080 ...")

	e.Logger.Fatal(e.Start(":1323"))
}
