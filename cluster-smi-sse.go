package main

import (
	"flag"
	"fmt"
	"encoding/json"
	"net/http"
	"github.com/minostauros/cluster-smi/cluster"
	"github.com/pebbe/zmq4"
	"github.com/vmihailenco/msgpack"
	"log"
	"os"
	"time"
)

// the amount of time to wait when pushing a message to
// a slow client or a client that closed after `range clients` started.
const patience time.Duration = time.Second*1

// Example SSE server in Golang.
//     $ go run sse.go

type Broker struct {

	// Events are pushed to this channel by the main events-gathering routine
	Notifier chan []byte

	// New client connections
	newClients chan chan []byte

	// Closed client connections
	closingClients chan chan []byte

	// Client connections registry
	clients map[chan []byte]bool

}

func NewServer() (broker *Broker) {
	// Instantiate a broker
	broker = &Broker{
		Notifier:       make(chan []byte, 1),
		newClients:     make(chan chan []byte),
		closingClients: make(chan chan []byte),
		clients:        make(map[chan []byte]bool),
	}

	// Set it running - listening and broadcasting events
	go broker.listen()

	return
}

func (broker *Broker) ServeHTTP(rw http.ResponseWriter, req *http.Request) {

	// Make sure that the writer supports flushing.
	//
	flusher, ok := rw.(http.Flusher)

	if !ok {
		http.Error(rw, "Streaming unsupported!", http.StatusInternalServerError)
		return
	}

	rw.Header().Set("Content-Type", "text/event-stream")
	rw.Header().Set("Cache-Control", "no-cache")
	rw.Header().Set("Connection", "keep-alive")
	rw.Header().Set("Access-Control-Allow-Origin", "*")

	// Each connection registers its own message channel with the Broker's connections registry
	messageChan := make(chan []byte)

	// Signal the broker that we have a new connection
	broker.newClients <- messageChan

	// Remove this client from the map of connected clients
	// when this handler exits.
	defer func() {
		broker.closingClients <- messageChan
	}()

	// Listen to connection close and un-register messageChan
	notify := rw.(http.CloseNotifier).CloseNotify()

	for {
		select {
		case <-notify:
			return
		default:

			// Write to the ResponseWriter
			// Server Sent Events compatible
			fmt.Fprintf(rw, "data: %s\n\n", <-messageChan)

			// Flush the data immediatly instead of buffering it for later.
			flusher.Flush()
		}
	}

}

func (broker *Broker) listen() {
	for {
		select {
		case s := <-broker.newClients:

			// A new client has connected.
			// Register their message channel
			broker.clients[s] = true
			log.Printf("Client added. %d registered clients", len(broker.clients))
		case s := <-broker.closingClients:

			// A client has dettached and we want to
			// stop sending them messages.
			delete(broker.clients, s)
			log.Printf("Removed client. %d registered clients", len(broker.clients))
		case event := <-broker.Notifier:

			// We got a new event from the outside!
			// Send event to all connected clients
			for clientMessageChan, _ := range broker.clients {
				select {
				case clientMessageChan <- event:
				case <-time.After(patience):
					log.Print("Skipping client.")
				}
			}
		}
	}

}

// dummy request for REQ-ROUTER pattern
type Request struct {
	Identity string
}

func RequestUpdateMessage() (buf []byte, err error) {
	id := fmt.Sprintf("REQ %v", os.Getpid())
	req := Request{id}
	return msgpack.Marshal(&req)
}

func main() {
	// SSE stands for Server Sent Events

	broker := NewServer()

	flag.Parse()

	request_attempts := 0

	// load ports and ip-address
	cfg := LoadConfig()

	// ask for updates messages (REQ-ROUTER)
	request_socket, err := zmq4.NewSocket(zmq4.REQ)
	if err != nil {
		log.Fatalf("Failed open Socket ZMQ: %s\n", err.Error())
		panic(err)
	}
	defer request_socket.Close()

	SocketAddr := "tcp://" + cfg.RouterIp + ":" + cfg.Ports.Clients
	request_socket.Connect(SocketAddr)
	go func() {
		for {

			// request new update
			msg, err := RequestUpdateMessage()
			if err != nil {
				log.Fatal("request messsage error:", err)
				panic(err)
			}
			_, err = request_socket.SendBytes(msg, 0)
			if err != nil {
				log.Fatal("sending request messsage error:", err)
				panic(err)
			}

			// response from cluster-smi-server
			s, err := request_socket.RecvBytes(0)
			if err != nil {
				log.Println(err)

				time.Sleep(10 * time.Second)
				request_attempts += 1

				if request_attempts == 0 {
					panic("too many request attempts yielding an error")
				}
				continue
			}

			var clus cluster.Cluster
			err = msgpack.Unmarshal(s, &clus)

			clus.Sort()

			clusjson, err := json.Marshal(clus)
			if err != nil {
				panic(err)
			}
			// Send JSON formatted object to clients
			broker.Notifier <- []byte(clusjson)
			
			time.Sleep(time.Duration(cfg.Tick) * time.Second)
			
		}
	}()

	if len(cfg.SSLCert) > 0 {
		err := http.ListenAndServeTLS(cfg.SSEIp + ":" + cfg.Ports.SSE, cfg.SSLCert, cfg.SSLKey, broker)
	    if err != nil {
	        log.Fatal("ListenAndServe: ", err)
	    }
	} else {
		log.Fatal("HTTP server error: ", http.ListenAndServe(cfg.SSEIp + ":" + cfg.Ports.SSE, broker))	
	}
	

}
