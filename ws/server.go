package ws

import (
	"database/sql"
	"encoding/json"
	"errors"
	contracts "exchange/Contracts"
	hub "exchange/Hub"
	symbolmanager "exchange/SymbolManager"
	"exchange/db"
	qdb "exchange/questdb"
	"fmt"
	_ "io"
	"log"
	"os/signal"
	"strings"
	"syscall"
	"time"
	"exchange/utils"
	"github.com/golang-jwt/jwt/v4"
	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5"

	"github.com/joho/godotenv"
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

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		// Allow localhost origins (adjust ports as needed)
		origin := r.Header.Get("Origin")
		return origin == "http://localhost:3000" ||
			origin == "http://127.0.0.1:3000" ||
			origin == "http://localhost:1323" ||
			origin == "http://stock-ex.aniketnegi.com:3000" ||
			origin == "http://stock-ex.aniketnegi.com" ||
			origin == "http://stock-ex.aniketnegi.com:1323"
	},
}
var googleOauthConfig *oauth2.Config

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
	cache                *balances.BalanceHoldingCache
	cacheGetBalanceCh    chan balances.GetBalanceReq
	cacheGetHoldingsCh   chan balances.GetHoldingsReq
	orderBuffer          chan shm.Order
	cancelBuffer		 chan shm.OrderToBeCanceled
	mmOrderBuffer        chan shm.Order
	mmCancelBuffer       chan shm.OrderToBeCanceled
	mmResponseBuffer     chan shm.Message
}

func NewServer(
	symbo_manager_ptr *symbolmanager.SymbolManager,
	order_events_hub_ptr *hub.OrderEventsHub,
	shm_manager_ptr *shm.ShmManager,
	cache *balances.BalanceHoldingCache,
) *Server {
	return &Server{
		symbol_manager_ptr:   symbo_manager_ptr,
		order_events_hub_ptr: order_events_hub_ptr,
		shm_manager_ptr:      shm_manager_ptr,
		cache:                cache,
		cacheGetBalanceCh:    make(chan balances.GetBalanceReq, 4096),
		cacheGetHoldingsCh:   make(chan balances.GetHoldingsReq, 4096),
		//we need to decide its capacity later
		orderBuffer: make(chan shm.Order, 32768),
		cancelBuffer: make(chan shm.OrderToBeCanceled, 32768),
		mmOrderBuffer: make(chan shm.Order, 32768),
		mmCancelBuffer: make(chan shm.OrderToBeCanceled, 32768),
		mmResponseBuffer: make(chan shm.Message, 32768),
	}
}

//poller that will deque from the shm queue and push into the order buffer

func (s *Server) PollMMOrders() {
	for {
		order, err := s.shm_manager_ptr.Incoming_MM_queue.Dequeue()
		if err != nil {
			fmt.Println("error dequeuing mm order:", err)
		}
		if order == nil {
			continue
		}

		order.Validate()
		
		if(order.OrderID == 0 && order.Order_type == 0){
			//post order
			//generate order id
			var or shm.Order
			
			or.OrderID = utils.NextOrderID()
			or.UserId = 0
			or.Price = order.Price
			or.Timestamp = order.Timestamp
			or.Quantity = order.Quantity
			or.Symbol = order.Symbol
			or.Side = order.Side
			or.Order_type = order.Order_type
			or.Status = 0 // pending

			s.mmOrderBuffer <- or

			var message shm.Message
			message.OrderId = or.OrderID
			message.ClientId = order.ClientID
			message.Timestamp = order.Timestamp
			message.Symbol = order.Symbol
			message.MessageType = 1 // order placed ack
			s.mmResponseBuffer <- message

		}else if(order.Order_type == 1){	
			//cancel order
			var or shm.OrderToBeCanceled
			or.OrderId = order.OrderID
			or.Symbol = order.Symbol
			or.UserId = 0
			s.mmCancelBuffer <- or

			var message shm.Message
			message.OrderId = order.OrderID
			message.ClientId = order.ClientID
			message.Timestamp = order.Timestamp
			message.Symbol = order.Symbol
			message.MessageType = 2 // order cancel ack
			s.mmResponseBuffer <- message
		}
	}
}

func(s *Server) MMResponsePoller(){
	for {
	 	message := <-s.mmResponseBuffer
			//enqueue into shm
			if err := s.shm_manager_ptr.MM_Response_queue.Enqueue(message); err != nil {
				log.Println("Failed to enqueue mm response message from buffer:", err)
		}
	}
}
// poller go routine to read from order buffer and enqueue into shm
func (s *Server) OrderBufferPoller() {
	for {
		select {
		case order := <-s.mmOrderBuffer:
			//enqueue into shm
			if err := s.shm_manager_ptr.Post_Order_queue.Enqueue(order); err != nil {
				log.Println("Failed to enqueue order from buffer:", err)
			}
		case order := <-s.orderBuffer:
			//enqueue into shm
			if err := s.shm_manager_ptr.Post_Order_queue.Enqueue(order); err != nil {
				log.Println("Failed to enqueue order from buffer:", err)
			}
		}
	}
}

func (s *Server) CancelBufferPoller() {
	for {
		select {
		case order := <-s.mmCancelBuffer:
			//enqueue into shm
			if err := s.shm_manager_ptr.CancelOrderQueue.Enqueue(order); err != nil {
				log.Println("Failed to enqueue cancel order from buffer:", err)
			}


		case order := <-s.cancelBuffer:
			//enqueue into shm
			if err := s.shm_manager_ptr.CancelOrderQueue.Enqueue(order); err != nil {
				log.Println("Failed to enqueue cancel order from buffer:", err)
			}
	}
}
}

func (s *Server) GetRecentTradesHandler(c echo.Context) error {
	symbol := c.QueryParam("symbol")
	if symbol == "" {
		return c.JSON(400, map[string]string{"error": "symbol is required"})
	}

	limit := 20
	if v := c.QueryParam("limit"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil {
			limit = parsed
		} else {
			return c.JSON(400, map[string]string{"error": "invalid limit"})
		}
	}

	trades, err := qdb.GetRecentTrades(c.Request().Context(), symbol, limit)
	if err != nil {
		return c.JSON(500, map[string]string{"error": err.Error()})
	}

	return c.JSON(200, trades)
}
func (s *Server) GetRecentSnapshotsHandler(c echo.Context) error {
	symbol := c.QueryParam("symbol")
	if symbol == "" {
		return c.JSON(400, map[string]string{"error": "symbol is required"})
	}

	limit := 20
	if v := c.QueryParam("limit"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil {
			limit = parsed
		} else {
			return c.JSON(400, map[string]string{"error": "invalid limit"})
		}
	}

	snaps, err := qdb.GetRecentOrderBookSnapshots(c.Request().Context(), symbol, limit)
	if err != nil {
		return c.JSON(500, map[string]string{"error": err.Error()})
	}

	return c.JSON(200, snaps)
}

func (s *Server) GetBalanceHandler(c echo.Context) error {
	userID := c.Get("user_id").(uint64)

	reqCtx := c.Request().Context()

	replyCh := make(chan shm.UserBalance, 1)

	req := balances.GetBalanceReq{
		UserID:  userID,
		ReplyCh: replyCh,
	}
	//send it in reader channel
	select {
	case s.cacheGetBalanceCh <- req:
	case <-reqCtx.Done():
		return echo.NewHTTPError(http.StatusRequestTimeout, "request cancelled")
	}
	//wait for reply or context done
	select {
	case balance := <-replyCh:
		return c.JSON(http.StatusOK, balance)
	case <-reqCtx.Done():
		return echo.NewHTTPError(http.StatusRequestTimeout, "request cancelled")
	}

}

func (s *Server) GetHoldingsHandler(c echo.Context) error {
	userID := c.Get("user_id").(uint64)

	reqCtx := c.Request().Context()

	replyCh := make(chan shm.UserHoldings, 1)
	req := balances.GetHoldingsReq{
		UserID:  userID,
		ReplyCh: replyCh,
	}
	//send it in reader channel
	select {
	case s.cacheGetHoldingsCh <- req:
	case <-reqCtx.Done():
		return echo.NewHTTPError(http.StatusRequestTimeout, "request cancelled")
	}
	//wait for reply or context done
	select {
	case holdings := <-replyCh:
		return c.JSON(http.StatusOK, holdings)
	case <-reqCtx.Done():
		return echo.NewHTTPError(http.StatusRequestTimeout, "request cancelled")
	}
}

func (s *Server) CancelOrderHandler(c echo.Context) error {
	userID := c.Get("user_id").(uint64)

	var tempOrderToBeCanceled shm.TempOrderToBeCanceled
	if err := c.Bind(&tempOrderToBeCanceled); err != nil {
		return c.JSON(400, map[string]string{"error": "Invalid request body"})
	}

	var cancelOrder shm.OrderToBeCanceled
	cancelOrder.OrderId = tempOrderToBeCanceled.OrderId
	cancelOrder.Symbol = tempOrderToBeCanceled.Symbol
	cancelOrder.UserId = userID

	//enqueue into cancel order shm queue
	select {
	case s.cancelBuffer <- cancelOrder:
	default:
		return echo.NewHTTPError(http.StatusServiceUnavailable, "Cancel order buffer full, try again later")
	}

	return c.JSON(http.StatusOK, map[string]interface{}{
		"status":   "Cancel order request placed successfully",
		"order_id": cancelOrder.OrderId,
		"user_id":  cancelOrder.UserId,
	})
}

func (s *Server) PostOrderHandler(c echo.Context) error {
	userID := c.Get("user_id").(uint64)
	///order id will be generated here

	var tempOrder shm.TempOrder
	if err := c.Bind(&tempOrder); err != nil {
		return c.JSON(400, map[string]string{"error": "Invalid request body"})
	}

	// Validate order fields
	if err := tempOrder.Validate(); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	
	var order shm.Order
	order.OrderID = utils.NextOrderID()
	order.Price = tempOrder.Price
	order.Timestamp = tempOrder.Timestamp
	order.UserId = userID
	order.Quantity = tempOrder.Quantity
	order.Symbol = tempOrder.Symbol
	order.Side = tempOrder.Side
	order.Order_type = tempOrder.Order_type
	order.Status = 0 // pending

	// Enqueue the order into order buffer
	select {
	case s.orderBuffer <- order:
	default:
		return echo.NewHTTPError(http.StatusServiceUnavailable, "Order buffer full, try again later")
	}

	// Asynchronously record the pending order in the database

	go func(order shm.Order) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		if err := db.RecordPendingOrder(ctx, order); err != nil {
			log.Printf("RecordPendingOrder failed: %v", err)
		}
	}(order)

	return c.JSON(http.StatusOK, map[string]interface{}{
		"status":   "Order placed successfully",
		"order_id": order.OrderID,
		"user_id":  order.UserId,
		"symbol":   order.Symbol,
	})
}

func (s *Server) GetUserOrdersHandler(c echo.Context) error {
	userID := c.Get("user_id").(uint64)
	limit := int32(50)
	offset := int32(0)

	orders, err := db.GetUserOrders(c.Request().Context(), userID, limit, offset)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	return c.JSON(http.StatusOK, orders)
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

// JWT Middleware
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
			if !ok {
				return echo.ErrUnauthorized
			}
			userID, err := strconv.ParseUint(userIDStr, 10, 64)
			if err != nil {
				return echo.ErrUnauthorized
			}
			c.Set("user_id", userID) // uint64

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
func (s *Server) googleCallback(c echo.Context) error {
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
	decoder := json.NewDecoder(resp.Body)
	if err := decoder.Decode(&userinfo); err != nil {
		return c.JSON(500, map[string]string{"error": "Decode userinfo failed"})
	}

	// Find or create user - use REAL Google user ID
	if s.shm_manager_ptr == nil {
		log.Println("SHM MANAGER PTR IS NIL IN GOOGLE CALLBACK")
	}

	user, err := db.FindOrCreateOAuthUser(ctx, "google", userinfo.ID, userinfo.Email, userinfo.Name, s.shm_manager_ptr)
	if err != nil {
		return c.JSON(500, map[string]string{"error": "User creation failed: " + err.Error()})
	}
	// Issue JWT
	jwtToken := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"user_id": strconv.FormatUint(uint64(user.ID), 10),
		"email":   *user.Email,
		"exp":     time.Now().Add(time.Hour * 24).Unix(),
	})
	jwtString, err := jwtToken.SignedString(jwtSecret)
	if err != nil {
		return c.JSON(500, map[string]string{"error": "JWT signing failed"})
	}

	// Redirect to frontend with JWT
	redirectURL := "http://stock-ex.aniketnegi.com:3000/dashboard?token=" + jwtString + "&user_id=" + strconv.FormatInt(user.ID, 10)
	return c.Redirect(http.StatusTemporaryRedirect, redirectURL)
	/* return c.JSON(http.StatusOK, map[string]any{
	"token":   jwtString,
	"user_id": strconv.FormatUint(uint64(user.ID), 10),
	"email":   user.Email, // keep as pointer if you want }*/

}
func adminOnlyMiddleware() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			v := c.Get("user_id")
			userID, ok := v.(uint64)
			if !ok {
				return echo.ErrUnauthorized
			}

			ctx := c.Request().Context()
			u, err := db.GetUserByID(ctx, int64(userID))
			if err != nil {
				if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, sql.ErrNoRows) {
					return echo.ErrUnauthorized
				}
				log.Printf("admin check failed for user %d: %v", userID, err) // Log for debugging
				return echo.NewHTTPError(http.StatusInternalServerError, "user lookup failed")
			}

			if !u.IsAdmin {
				return echo.NewHTTPError(http.StatusForbidden, "admin access required")
			}

			return next(c)
		}
	}
}

// admin handlers
func (s *Server) PromoteUserToAdminHandler(c echo.Context) error {
	targetUserIDStr := c.Param("userID")
	targetUserID, err := strconv.ParseInt(targetUserIDStr, 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid user ID")
	}

	ctx := c.Request().Context()
	if err := db.SetUserAdmin(ctx, targetUserID, true); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to promote user")
	}

	return c.JSON(http.StatusOK, map[string]interface{}{
		"message":  "User promoted to admin",
		"user_id":  targetUserID,
		"is_admin": true,
	})
}

func (s *Server) DemoteUserToAdminHandler(c echo.Context) error {
	targetUserIDStr := c.Param("userID")
	targetUserID, err := strconv.ParseInt(targetUserIDStr, 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid user ID")
	}

	ctx := c.Request().Context()
	if err := db.SetUserAdmin(ctx, targetUserID, false); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to demote user")
	}

	return c.JSON(http.StatusOK, map[string]interface{}{
		"message":  "User demoted to admin",
		"user_id":  targetUserID,
		"is_admin": false,
	})
}

func (s *Server) ListUsersHandler(c echo.Context) error {
	limit := int32(50)
	offset := int32(0)
	if v := c.QueryParam("limit"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil {
			limit = int32(parsed)
		}
	}
	users, err := db.ListUsers(c.Request().Context(), limit, offset)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to list users")
	}
	return c.JSON(http.StatusOK, users)
}

func (s *Server) CreateServer() {
	fmt.Println("BOOTING SERVER...")
	// Load .env
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found - using system env")
	}

	// Create config NOW (after .env loaded)
	googleOauthConfig = &oauth2.Config{
		ClientID:     os.Getenv("GOOGLE_CLIENT_ID"),
		ClientSecret: os.Getenv("GOOGLE_CLIENT_SECRET"),
		RedirectURL:  "http://stock-ex.aniketnegi.com:1323/auth/google/callback",
		Scopes:       []string{"openid", "email", "profile"},
		Endpoint:     google.Endpoint,
	}
	if googleOauthConfig.ClientID == "" {
		log.Fatal(" GOOGLE_CLIENT_ID missing!")
	}
	fmt.Println("Google OAuth ready:", googleOauthConfig.ClientID)

	//init db
	if err := db.InitDB("postgres://stock_user:stock_pass@localhost:5432/stock_exchange?sslmode=disable"); err != nil {
		log.Fatalf("DB init failed: %v", err)
	}
	// init questdb
	if err := qdb.InitQuestDB("postgresql://admin:quest@localhost:8812/qdb?sslmode=disable"); err != nil {
		log.Fatalf("QuestDB init failed: %v", err)
	}
	// start balance polling go routines
	go s.OrderBufferPoller()
	go s.CancelBufferPoller()
	go s.MMResponsePoller()
	go s.PollMMOrders()

	ctx := context.Background()

	go s.cache.Updater()
	go s.cache.RunReader(ctx, s.cacheGetBalanceCh, s.cacheGetHoldingsCh)

	go balances.PollBalanceResponses(s.shm_manager_ptr.Balance_Response_queue)
	go balances.PollHoldingResponses(s.shm_manager_ptr.Holding_Response_queue)

	e := echo.New()

	// CORS + Middleware
	e.Use(middleware.CORSWithConfig(middleware.CORSConfig{
		AllowOrigins: []string{"http://localhost:3000", "http://127.0.0.1:3000", "http://localhost:1323", "http://stock-ex.aniketnegi.com:3000"},
		AllowMethods: []string{echo.GET, echo.POST, echo.DELETE},
		AllowHeaders: []string{echo.HeaderAuthorization, echo.HeaderContentType},
	}))
	e.Use(middleware.RequestLogger())
	e.Use(middleware.Recover())
	e.Use(middleware.RecoverWithConfig(middleware.RecoverConfig{
		DisablePrintStack: false, // ⭐ SHOW FULL STACK TRACE
	}))

	e.GET("/auth/google", googleAuthRedirect)
	//e.GET("/auth/github", githubAuthRedirect)

	// OAuth Callbacks - Exchange code → JWT
	e.GET("/auth/google/callback", s.googleCallback)
	//e.GET("/auth/github/callback", githubCallback)

	api := e.Group("/api")
	api.Use(jwtMiddleware())

	api.POST("/order", s.PostOrderHandler)
	api.GET("/balance", s.GetBalanceHandler)
	api.GET("/holdings", s.GetHoldingsHandler)
	api.DELETE("/cancel", s.CancelOrderHandler)
	api.GET("/orders", s.GetUserOrdersHandler)
	api.GET("/bootstrap/trades", s.GetRecentTradesHandler)
	api.GET("/bootstrap/snapshots", s.GetRecentSnapshotsHandler)

	admin := api.Group("/admin")
	admin.Use(adminOnlyMiddleware())
	// admin routes go here
	admin.POST("/users/promote/:userID", s.PromoteUserToAdminHandler)
	admin.POST("/users/demote/:userID", s.DemoteUserToAdminHandler)
	admin.GET("/users", s.ListUsersHandler) // list users

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
	qdb.CloseQuestDB()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	e.Shutdown(ctx)
	cancel()
}
