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
	DeleteType      *string `json:"deleteType"`
	PostType        string  `json:"postType"`
	PostID          *int    `json:"postID"`
	InvokerID       string  `json:"invokerID"`
	CommentBody     *string `json:"commentBody"`
	ParentCommentID *int    `json:"parentCommentID"`
	CommentID       *int    `json:"commentID"`
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

// for debugging
func PrintClients(clients []*Client) {
	lock.RLock()
	defer lock.RUnlock()
	for _, client := range clients {
		log.Println("ID:", &client.ID)
		log.Println("Connection:", &client.Conn)
		log.Println("ChannelID:", &client.ChannelID)
		log.Println("ChannelType:", &client.ChannelType)
		log.Println()
	}
}
func PrintClientsMap() {
	lock.RLock()
	defer lock.RUnlock()
	for key, client := range clients {
		log.Println("Client Key:", key)
		log.Println("ID:", client.ID)
		log.Println("Connection:", &client.Conn)
		log.Println("ChannelID:", client.ChannelID)
		log.Println("ChannelType:", client.ChannelType)
		log.Println()
	}
}

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
	noParentSafe := -1
	if data.ParentCommentID != nil {
		noParentSafe = *data.ParentCommentID
	}
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
		Action        string `json:"action"`
		CommentID     int64  `json:"commentID"`
		CommentParent int    `json:"commentParent"`
		CommentBody   string `json:"commentBody"`
		CommenterID   string `json:"commenterID"`
	}{
		Action:        "commentCreationBroadcast",
		CommentID:     commentID,
		CommentParent: noParentSafe,
		CommentBody:   *data.CommentBody,
		CommenterID:   data.InvokerID,
	})

	if err != nil {
		log.Printf("Failed to create JSON message: %v", err)
		return
	}
	broadcast(jsonMsg, broadcastTargets)
}

func commentUpdate(data Data) {
	const query = `UPDATE Comment SET body = ?, edited = ? WHERE id = ?`
	params := []interface{}{
		data.CommentBody,
		true,
		data.CommentID,
	}
	_, err := db.Exec(query, params...)
	if err != nil {
		log.Printf("Failed to execute query: %v", err)
		return
	}
	broadcastTargets := getAllConnectionsInChannel(*data.PostID, data.PostType)
	jsonMsg, err := json.Marshal(&struct {
		Action      string `json:"action"`
		CommentID   int    `json:"commentID"`
		CommentBody string `json:"commentBody"`
	}{
		Action:      "commentUpdateBroadcast",
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
	if *data.DeleteType == "user" || (*data.DeleteType == "admin" && data.InvokerID == os.Getenv("ADMIN_ID")) {
		var params []interface{}
		var query string
		deletionBody := fmt.Sprintf("[comment removed by %s]", *data.DeleteType)

		if *data.DeleteType == "user" {
			query = `UPDATE Comment SET body = ?, edited = ? WHERE id = ?`
			params = []interface{}{
				deletionBody,
				false,
				data.CommentID,
			}
		} else {
			query = `UPDATE Comment SET body = ?, edited = ?, commenter_id = ? WHERE id = ?`
			params = []interface{}{
				deletionBody,
				false,
				0,
				data.CommentID,
			}
		}
		_, err := db.Exec(query, params...)
		if err != nil {
			log.Printf("Failed to execute query: %v", err)
			return
		}

		broadcastTargets := getAllConnectionsInChannel(*data.PostID, data.PostType)
		jsonMsg, err := json.Marshal(&struct {
			Action      string `json:"action"`
			CommentID   int    `json:"commentID"`
			CommentBody string `json:"commentBody"`
		}{
			Action:      "commentDeletionBroadcast",
			CommentID:   *data.CommentID,
			CommentBody: deletionBody,
		})

		if err != nil {
			log.Printf("Failed to create JSON message: %v", err)
			return
		}
		broadcast(jsonMsg, broadcastTargets)
	} else if *data.DeleteType == "full" {
		const query = `DELETE FROM Comment WHERE id = ?`
		_, err := db.Exec(query, data.CommentID)
		if err != nil {
			log.Printf("Failed to execute query %v", err)
			return
		}
		broadcastTargets := getAllConnectionsInChannel(*data.PostID, data.PostType)
		jsonMsg, err := json.Marshal(&struct {
			Action    string `json:"action"`
			CommentID int    `json:"commentID"`
		}{
			Action:    "commentDeletionBroadcast",
			CommentID: *data.CommentID,
		})
		if err != nil {
			log.Printf("Failed to create JSON message: %v", err)
			return
		}
		broadcast(jsonMsg, broadcastTargets)
	}
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
			log.Printf("Unrecognized action: %s", data.Action)
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
	reader(client)
}

func setupRoutes() {
	http.HandleFunc("/", wsEndpoint)
}

func main() {
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
