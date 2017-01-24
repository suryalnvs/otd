package main

import (
        "flag"
        "fmt"
        "math"
        "os/exec"
        "log"
        "time"

        "github.com/hyperledger/fabric/orderer/common/bootstrap/provisional"
        "github.com/hyperledger/fabric/orderer/localconfig"
        cb "github.com/hyperledger/fabric/protos/common"
        ab "github.com/hyperledger/fabric/protos/orderer"
        "github.com/hyperledger/fabric/protos/utils"
        "golang.org/x/net/context"
        "google.golang.org/grpc"
)

type ordererdriveClient struct {
        client  ab.AtomicBroadcast_DeliverClient
        chainID string
}
func newOrdererdriveClient(client ab.AtomicBroadcast_DeliverClient, chainID string) *ordererdriveClient {
        return &ordererdriveClient{client: client, chainID: chainID}
}

func seekHelper(chainID string, start *ab.SeekPosition) *cb.Envelope {
        return &cb.Envelope{
                Payload: utils.MarshalOrPanic(&cb.Payload{
                        Header: &cb.Header{
                                ChainHeader: &cb.ChainHeader{
                                        ChainID: chainID,
                                },
                                SignatureHeader: &cb.SignatureHeader{},
                        },

                        Data: utils.MarshalOrPanic(&ab.SeekInfo{
                                Start:    &ab.SeekPosition{Type: &ab.SeekPosition_Oldest{Oldest: &ab.SeekOldest{}}},
                                Stop:     &ab.SeekPosition{Type: &ab.SeekPosition_Specified{Specified: &ab.SeekSpecified{Number: math.MaxUint64}}},
                                Behavior: ab.SeekInfo_BLOCK_UNTIL_READY,
                        }),
                }),
        }
}

func (r *ordererdriveClient) seekOldest() error {
        return r.client.Send(seekHelper(r.chainID, &ab.SeekPosition{Type: &ab.SeekPosition_Oldest{Oldest: &ab.SeekOldest{}}}))
}

func (r *ordererdriveClient) seekNewest() error {
        return r.client.Send(seekHelper(r.chainID, &ab.SeekPosition{Type: &ab.SeekPosition_Newest{Newest: &ab.SeekNewest{}}}))
}

func (r *ordererdriveClient) seek(blockNumber uint64) error {
        return r.client.Send(seekHelper(r.chainID, &ab.SeekPosition{Type: &ab.SeekPosition_Specified{Specified: &ab.SeekSpecified{Number: blockNumber}}}))
}

func cleannetwork() {
    cmd :="docker rm -f $(docker ps -aq)"
    out, err := exec.Command("/bin/sh", "-c", cmd).Output()
    if (err != nil) {
      fmt.Println("Removed the Network")
      fmt.Println(out)
      log.Fatal(err)
    } //else {
      //fmt.Println("output", out)
    //}
}
func launchnetwork() {
		cmd :="docker-compose up -d"
		out, err := exec.Command("/bin/sh", "-c", cmd).Output()
		if (err != nil) {
			fmt.Println("StartNetworkLocal: Could not exec docker start ")
			log.Fatal(err)
      fmt.Println(out)
		}
}
func network() {
    cmd :="docker ps -a"
    doc, err := exec.Command("/bin/sh", "-c", cmd).Output()
    if (err != nil) {
      fmt.Println("Unable to launch the network ", err)
      log.Fatal(err)
    } else {
      fmt.Println("network is ", string(doc))
    }
}
func (r *ordererdriveClient) readUntilClose() {
        //totaltx = 0
        for {
                msg, err := r.client.Recv()
                if err != nil {
                        fmt.Println("Error receiving:", err)
                        return
                }

                switch t := msg.Type.(type) {
                case *ab.DeliverResponse_Status:
                        fmt.Println("Got status ", t)
                        return
                case *ab.DeliverResponse_Block:
                        ntotal()
                        //totaltx += int64(len(t.Block.Data.Data))
			                  fmt.Println("Received block number: ", t.Block.Header.Number, " Transactions of the block: ", len(t.Block.Data.Data), "Total Transactions: ", totaltx)
                }
        }
}
func ntotal() {
        totaltx += int64(len(t.Block.Data.Data))
}
func getntotal() totaltx{
        return totaltx
}
func deliverywatcher() {
        config := config.Load()
        //go launchnetwork()
        var chainID string
        var serverAddr string

		    //fmt.Println("After start orderers, sleep 5 secs")
		    time.Sleep(5000 * time.Millisecond)
        flag.StringVar(&serverAddr, "server", fmt.Sprintf("%s:%d", config.General.ListenAddress, config.General.ListenPort), "The RPC server to connect to.")
        flag.StringVar(&chainID, "chainID", provisional.TestChainID, "The chain ID to deliver from.")
        flag.Parse()

        conn, err := grpc.Dial(serverAddr, grpc.WithInsecure())
        if err != nil {
                fmt.Println("Error connecting:", err)
                return
        }
        client, err := ab.NewAtomicBroadcastClient(conn).Deliver(context.TODO())
        if err != nil {
                fmt.Println("Error connecting:", err)
                return
        }

        s := newOrdererdriveClient(client, chainID)
        err = s.seekNewest()
        if err == nil {
                fmt.Println("Received error:", err)
        }
        s.readUntilClose()
}
var totaltx int64
func main(){
  cleannetwork()
  fmt.Println("==================Starting the network=========================")
  go launchnetwork()
  fmt.Println("After start orderers, sleep 10 secs")
  time.Sleep(10000 * time.Millisecond)
  fmt.Println("Printing the network")
  network()
  fmt.Println("===================Deliver Client=============================")
  go deliverywatcher()
  fmt.Println("transactions", totaltx)
}
