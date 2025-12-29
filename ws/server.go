package ws

import (
	"encoding/json"
	contracts "exchange/Contracts"
	hub "exchange/Hub"
	symbolmanager "exchange/SymbolManager"
	"exchange/db"
	"fmt"
	"log"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/golang-jwt/jwt/v4"
	"github.com/gorilla/websocket"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"

	"context"
	"exchange/balances"
	"exchange/shm"
	"net/http"
	"os"
	"strconv" // for cookie

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
)

var googleOauthConfig = &oauth2.Config{
	ClientID:     os.Getenv("GOOGLE_CLIENT_ID"),
	ClientSecret: os.Getenv("GOOGLE_CLIENT_SECRET"),
	RedirectURL:  "http://localhost:1323/auth/google/callback",
	Scopes:       []string{"openid", "email", "profile"},
	Endpoint:     google.Endpoint,
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		// Allow localhost origins (adjust ports as needed)
		origin := r.Header.Get("Origin")
		return origin == "http://localhost:3000" ||
			origin == "http://127.0.0.1:3000" ||
			origin == "http://localhost:1323"
	},
}

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
	userID := c.Get("user_id").(uint64)

	snap := balances.GetCurrentSnapshot()
	bal, ok := snap.Balances[userID]
	if !ok {
		bal = shm.UserBalance{UserId: userID} // default zero balance
	}

	return c.JSON(http.StatusOK, bal)
}

func (s *Server) GetHoldingsHandler(c echo.Context) error {
	userID := c.Get("user_id").(uint64)

	snap := balances.GetCurrentSnapshot()
	h, ok := snap.Holdings[userID]
	if !ok {
		h = shm.UserHoldings{UserId: userID} // default empty holdings
	}

	return c.JSON(http.StatusOK, h)
}

func (s *Server) CancelOrderHandler(c echo.Context) error {
	userID := c.Get("user_id").(uint64)

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
	userID := c.Get("user_id").(uint64)

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
		if err := coe.Conn.WriteMessage(websocket.TextMessage, message); err != nil {
			return
		}
	}
}

func (s *Server) wsHandlerOrderEvents(c echo.Context) error {
	userID := c.Get("user_id").(uint64)

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

// JWT Middleware
var jwtSecret = []byte("your-super-secret-jwt-key-change-in-prod") // Or load from env

// JWT Middleware - Replaces echoserver.TokenHandler()
func jwtMiddleware() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			authHeader := c.Request().Header.Get("Authorization")
			if authHeader == "" {
				return echo.ErrUnauthorized
			}

			tokenStr := strings.TrimPrefix(authHeader, "Bearer ")
			token, err := jwt.Parse(tokenStr, func(token *jwt.Token) (interface{}, error) {
				return jwtSecret, nil
			})
			if err != nil || !token.Valid {
				return echo.ErrUnauthorized
			}

			claims, ok := token.Claims.(jwt.MapClaims)
			if !ok {
				return echo.ErrUnauthorized
			}

			userIDStr, ok := claims["user_id"].(string)
			if !ok { return echo.ErrUnauthorized }
			userID, err := strconv.ParseUint(userIDStr, 10, 64)
			if err != nil { return echo.ErrUnauthorized }
			c.Set("user_id", userID)  // uint64 

			return next(c)
		}
	}
}

// Google OAuth Redirect
func googleAuthRedirect(c echo.Context) error {
    state := fmt.Sprintf("%d", time.Now().UnixNano())
    c.SetCookie(&http.Cookie{
        Name:  "oauth_state",
        Value: state,
        Path:  "/",
    })
    url := googleOauthConfig.AuthCodeURL(state) 
    return c.Redirect(http.StatusTemporaryRedirect, url)
}


// Google Callback → FindOrCreateUser → Issue JWT
func googleCallback(c echo.Context) error {
	code := c.QueryParam("code")
	if code == "" {
		return c.JSON(400, map[string]string{"error": "Missing code"})
	}

	ctx := c.Request().Context() 

	// Exchange code for tokens
	oauthToken, err := googleOauthConfig.Exchange(ctx, code)
	if err != nil {
		return c.JSON(500, map[string]string{"error": "Token exchange failed: " + err.Error()})
	}

	// Get user info
	client := googleOauthConfig.Client(ctx, oauthToken)
	resp, err := client.Get("https://www.googleapis.com/oauth2/v2/userinfo")
	if err != nil {
		return c.JSON(500, map[string]string{"error": "Userinfo failed: " + err.Error()})
	}
	defer resp.Body.Close()

	var userinfo struct {
		ID    string `json:"id"`
		Email string `json:"email"`
		Name  string `json:"name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&userinfo); err != nil {
		return c.JSON(500, map[string]string{"error": "Decode userinfo failed"})
	}

	// Find or create user - use REAL Google user ID
	user, err := db.FindOrCreateOAuthUser(ctx, "google", userinfo.ID, userinfo.Email, userinfo.Name)
	if err != nil {
		return c.JSON(500, map[string]string{"error": "User creation failed: " + err.Error()})
	}
	// Issue JWT
	jwtToken := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
    "user_id": strconv.FormatUint(uint64(user.ID), 10),  // STRING "123"
    "email":   *user.Email,
    "exp":     time.Now().Add(time.Hour * 24).Unix(),
	})
	jwtString, err := jwtToken.SignedString(jwtSecret)
	if err != nil {
		return c.JSON(500, map[string]string{"error": "JWT signing failed"})
	}

	// Redirect to frontend with JWT
	redirectURL := "http://localhost:3000/dashboard?token=" + jwtString + "&user_id=" + strconv.FormatInt(user.ID, 10)
	return c.Redirect(http.StatusTemporaryRedirect, redirectURL)
}

func (s *Server) CreateServer() {
	fmt.Println("BOOTING SERVER...")

	//init db
	if err := db.InitDB("postgres://stock_user:stock_pass@localhost:5432/stock_exchange?sslmode=disable"); err != nil {
		log.Fatalf("DB init failed: %v", err)
	}
	balances.InitState()

	go balances.PollBalanceResponses(s.shm_manager_ptr.Balance_Response_queue)
	go balances.PollHoldingResponses(s.shm_manager_ptr.Holding_Response_queue)
	go balances.StateUpdater()

	e := echo.New()

	// CORS + Middleware
	e.Use(middleware.CORSWithConfig(middleware.CORSConfig{
		AllowOrigins: []string{"http://localhost:3000", "http://127.0.0.1:3000"},
		AllowMethods: []string{echo.GET, echo.POST},
	}))
	e.Use(middleware.RequestLogger())
	e.Use(middleware.Recover())

	
	e.GET("/auth/google", googleAuthRedirect)
	//e.GET("/auth/github", githubAuthRedirect)

	// OAuth Callbacks - Exchange code → JWT
	e.GET("/auth/google/callback", googleCallback)
	//e.GET("/auth/github/callback", githubCallback)

	
	api := e.Group("/api")
	api.Use(jwtMiddleware()) 

	api.POST("/order", s.PostOrderHandler)
	api.GET("/balance", s.GetBalanceHandler)
	api.GET("/holdings", s.GetHoldingsHandler)
	api.DELETE("/cancel", s.CancelOrderHandler)

	ws := e.Group("/ws")
	ws.Use(jwtMiddleware())
	ws.GET("/marketData", s.wsHandlerMd)
	ws.GET("/OrderEvents", s.wsHandlerOrderEvents)

	go func() {
		if err := e.Start(":1323"); err != nil && err != http.ErrServerClosed {
			log.Fatal("Server:", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	<-quit
	log.Println("Shutting down...")
	db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	e.Shutdown(ctx)  // Remove defer
	cancel()
}
