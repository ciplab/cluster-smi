package main

import (
	"flag"
	"fmt"
	"github.com/minostauros/cluster-smi/cluster"
	"github.com/pebbe/zmq4"
	"github.com/vmihailenco/msgpack"
	"log"
	"os"
	"time"
)

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

	showJustOnce := flag.Bool("once", false, "Show result just once")
	showTimePtr := flag.Bool("t", false, "show time of events")
	showExtendedPtr := flag.Bool("e", false, "extended view")
	showProcessesPtr := flag.Bool("p", false, "verbose process information")
	showContainerPtr := flag.Bool("c", false, "show Docker container")
	showShortPtr := flag.Bool("s", false, "show result w/o command, gpu mem, and runtime")
	nodeRegex := flag.String("n", ".", "match node-names with regex for display information "+
		"(if not specified, all nodes will be shown)")
	usernameFilter := flag.String("u", "", "show all information only for specific user")
	useColor := flag.Bool("color", true, "use colored output")
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

		if *usernameFilter != "" {
			clus = cluster.FilterByUser(clus, *usernameFilter)
		}

		clus.Sort()
		clus.FilterNodes(*nodeRegex)
		if *showShortPtr {
			*showContainerPtr = true
		}
		if *showProcessesPtr {
			*showContainerPtr = true
			*showShortPtr = true
		}
		clus.Print(*showProcessesPtr, *showTimePtr, cfg.Timeout, *useColor, *showExtendedPtr, *showShortPtr, *showContainerPtr)
		
		if *showJustOnce {
			break
		}
		time.Sleep(time.Duration(cfg.Tick) * time.Second)
		
	}

}
