package db

import (
	"database/sql"

	_ "github.com/mattn/go-sqlite3" // SQLite driver
)

type User struct {
	ID       uint64  `json:"id"`
	Username string  `json:"username"`
	Balance  float64 `json:"balance"`
}

var db *sql.DB

func InitDB() {
	var err error
	db, err = sql.Open("sqlite3", "users.db")
	if err != nil {
		panic(err)
	}

	// Create table if not exists
	_, err = db.Exec(`
        CREATE TABLE IF NOT EXISTS users (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            username TEXT UNIQUE NOT NULL,
            balance REAL DEFAULT 0,
            created_at DATETIME DEFAULT CURRENT_TIMESTAMP
        )
    `)
	if err != nil {
		panic(err)
	}
}

// 1. FIND EXISTING USER BY USERNAME
func FindUserByUsername(username string) (*User, error) {
	user := &User{}
	err := db.QueryRow(
		"SELECT id, username, balance FROM users WHERE username = ?",
		username,
	).Scan(&user.ID, &user.Username, &user.Balance)

	if err != nil {
		return nil, err // Returns sql.ErrNoRows if not found
	}
	return user, nil
}

// 2. FIND USER BY ID (USED BY CreateUser)
func FindUserByID(id int64) (*User, error) {
	user := &User{}
	err := db.QueryRow(
		"SELECT id, username, balance FROM users WHERE id = ?",
		id,
	).Scan(&user.ID, &user.Username, &user.Balance)

	if err != nil {
		return nil, err
	}
	return user, nil
}

// 3. CREATE NEW USER (auto-generates ID)
func CreateUser(username string, balance float64) (*User, error) {
	result, err := db.Exec(
		"INSERT INTO users (username, balance) VALUES (?, ?)",
		username, balance,
	)
	if err != nil {
		return nil, err
	}

	id, _ := result.LastInsertId() // Gets auto-generated ID

	//here only we add a queury to add user to balance manager
	/* var query structs.Query
	query.Query_id = 0   //deafult for new logins
	query.Query_type = 2 // add user on login
	query.User_id = uint64(id)
	// Enqueue the query
	if queue.QueriesQueue != nil {
		queue.QueriesQueue.Enqueue(query)
	} else {
		log.Println("Warning: QueriesQueue is nil")
	} */
	return FindUserByID(id) // Fetch complete user record
}
