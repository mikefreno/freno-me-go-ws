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
	PostID          *int    `json:"post_id"`
	InvokerID       string  `json:"invoker_id"`
	CommentBody     *string `json:"comment_body"`
	ParentCommentID *int    `json:"parent_comment_id"`
	CommentID       *int    `json:"comment_id"`
}

type Client struct {
	ID          string
	Conn        *websocket.Conn
	ChannelID   *int
	ChannelType *string
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

func getAllConnectionsInChannel(post_id int, post_type string) []*Client {
	lock.RLock()
	defer lock.RUnlock()
	var clientsInChannel []*Client
	for _, client := range clients {
		if client.ChannelID != nil && client.ChannelType != nil &&
			*client.ChannelID == post_id && *client.ChannelType == post_type {
			clientsInChannel = append(clientsInChannel, client)
		}
	}
	return clientsInChannel
}

func broadcast(message []byte, clients []*Client) {
	for _, client := range clients {
		err := client.Conn.WriteMessage(websocket.TextMessage, message)
		if err != nil {
			fmt.Println("write error:", err)
			client.Conn.Close()
		}
	}
}
func channelUpdate(data Data, client *Client) {
	lock.Lock()
	defer lock.Unlock()

	client, exists := clients[client.ID]
	if exists {
		client.ChannelType = &data.PostType
		client.ChannelID = data.PostID
	}
}

func commentCreation(data Data) {
	query := fmt.Sprintf(`INSERT INTO Comment (body, %s, parent_comment_id, commenter_id) VALUES (?, ?, ?, ?)`, (data.PostType + "_id"))
	params := []interface{}{
		data.CommentBody,
		data.PostID,
		data.ParentCommentID,
		data.InvokerID,
	}
	res, err := db.Exec(query, params...)
	if err != nil {
		log.Printf("Failed to execute query: %v", err)
		return
	}
	commentID, err := res.LastInsertId()
	if err != nil {
		log.Printf("Failed to retrieve ID: %v", err)
		return
	}
	broadcastTargets := getAllConnectionsInChannel(*data.PostID, data.PostType)
	jsonMsg, err := json.Marshal(&struct {
		CommentID   int64  `json:"comment_id"`
		CommentBody string `json:"comment_body"`
	}{
		CommentID:   *&commentID,
		CommentBody: *data.CommentBody,
	})

	if err != nil {
		log.Printf("Failed to create JSON message: %v", err)
		return
	}
	broadcast(jsonMsg, broadcastTargets)
}
func commentUpdate(data Data) {
	const query = `UPDATE Comment SET body = ? WHERE id = ?`
	params := []interface{}{
		data.CommentBody,
		data.CommentID,
	}
	_, err := db.Exec(query, params...)
	if err != nil {
		log.Printf("Failed to execute query: %v", err)
		return
	}
	broadcastTargets := getAllConnectionsInChannel(*data.PostID, data.PostType)
	jsonMsg, err := json.Marshal(&struct {
		CommentID   int    `json:"comment_id"`
		CommentBody string `json:"comment_body"`
	}{
		CommentID:   *data.CommentID,
		CommentBody: *data.CommentBody,
	})

	if err != nil {
		log.Printf("Failed to create JSON message: %v", err)
		return
	}
	broadcast(jsonMsg, broadcastTargets)
}

func commentDeletion(data Data) {
	const query = `UPDATE Comment SET body = ?, commenter_id = ? WHERE id = ?`
	deletionBody := "[comment removed by user]"
	params := []interface{}{
		deletionBody,
		0,
		data.CommentID,
	}
	_, err := db.Exec(query, params...)
	if err != nil {
		log.Printf("Failed to execute query: %v", err)
		return
	}
	broadcastTargets := getAllConnectionsInChannel(*data.PostID, data.PostType)
	jsonMsg, err := json.Marshal(&struct {
		CommentID   int    `json:"comment_id"`
		CommentBody string `json:"comment_body"`
	}{
		CommentID:   *data.CommentID,
		CommentBody: deletionBody,
	})

	if err != nil {
		log.Printf("Failed to create JSON message: %v", err)
		return
	}
	broadcast(jsonMsg, broadcastTargets)
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
			channelUpdate(data, client)
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
