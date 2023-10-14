package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	_ "github.com/go-sql-driver/mysql"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/joho/godotenv"
	"log"
	"net/http"
	"os"
	"sync"
)

type Data struct {
	Action          string  `json:"action"`
	PostType        string  `json:"postType"`
	BlogID          *int    `json:"blog_id"`
	ProjectID       *int    `json:"project_id"`
	InvokerID       string  `json:"invoker_id"`
	CommentBody     *string `json:"comment_body"`
	CommentType     *string `json:"comment_type"`
	ParentCommentID *int    `json:"parent_comment_id"`
}

type Client struct {
	ID   string
	Conn *websocket.Conn
}

var clients = make(map[string]*Client)
var lock = sync.RWMutex{}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
}

var db *sql.DB

func createDBConnection() *sql.DB {
	err := godotenv.Load()
	if err != nil {
		log.Printf("Error loading .env file")
	}

	db, err := sql.Open("mysql", os.Getenv("DSN"))
	if err != nil {
		log.Fatalf("failed to connect: %v", err)
	}

	if err := db.Ping(); err != nil {
		log.Fatalf("failed to ping: %v", err)
	}
	return db
}

func channelUpdate(data Data, connID string) {
	query := `INSERT INTO Connection (user_id, connection_id) VALUES(?, ?)`
	params := []interface{}{data.InvokerID, connID}
	_, err := db.Exec(query, params...)
	if err != nil {
		log.Printf("Failed to execute query: %v", err)
		return
	}
}
func commentCreation(data Data) {
	query := `INSERT INTO Comment (body, blog_id, project_id, parent_comment_id, commenter_id) VALUES (?, ?, ?, ?, ?)`
	params := []interface{}{
		data.CommentBody,
		data.BlogID,
		data.ProjectID,
		data.ParentCommentID,
		data.InvokerID,
	}
	_, err := db.Exec(query, params...)
	if err != nil {
		log.Printf("Failed to execute query: %v", err)
		return
	}

}
func commentUpdate(data Data) {

}
func commentDeletion(data Data) {

}

func reader(client *Client) {
	for {
		messageType, p, err := client.Conn.ReadMessage()
		if err != nil {
			lock.Lock()
			delete(clients, client.ID)
			lock.Unlock()

			client.Conn.Close()
			log.Println(err)
			return
		}
		jsonData := string(p)
		var data Data

		parse_err := json.Unmarshal([]byte(jsonData), &data)
		if parse_err != nil {
			log.Printf("Error occurred during unmarshaling. Error: %s", err.Error())
		}

		log.Println(data.Action)
		switch data.Action {
		case "channelUpdate":
			channelUpdate(data, client.ID)
		case "commentCreation":
			commentCreation(data)
		case "commentUpdate":
			commentUpdate(data)
		case "commentDeletion":
			commentDeletion(data)
		default:
			log.Println("Unrecognized action")
		}

		if err := client.Conn.WriteMessage(messageType, p); err != nil {
			log.Println(err)
			return
		}
	}
}

func wsEndpoint(writer http.ResponseWriter, req *http.Request) {
	upgrader.CheckOrigin = func(req *http.Request) bool { return true }

	ws, err := upgrader.Upgrade(writer, req, nil)
	if err != nil {
		log.Println(err)
	}
	client := &Client{
		ID:   uuid.New().String(),
		Conn: ws,
	}
	lock.Lock()
	clients[client.ID] = client
	lock.Unlock()
	log.Println("new connection: ", client.ID)
	reader(client)
}

func setupRoutes() {
	http.HandleFunc("/", wsEndpoint)
}

func main() {
	fmt.Println("Go websocket")
	db = createDBConnection()
	defer db.Close()
	setupRoutes()

	err := godotenv.Load()
	if err != nil {
		log.Printf("Error loading .env file")
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080" // Default port if not specified
	}
	log.Fatal(http.ListenAndServe(":"+port, nil))

}
