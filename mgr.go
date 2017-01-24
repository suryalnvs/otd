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

func (r *ordererdriveClient) readUntilClose(consumerNumber int) {
        var totaltx int64
        totaltx = 0
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
                        txRecv[consumerNumber] += int64(len(t.Block.Data.Data))
			                  fmt.Println("Received block number: ", t.Block.Header.Number, " Transactions of the block: ", len(t.Block.Data.Data), "Total Transactions: ", totaltx)
nt64                }
        }
}

func startConsumer(serverAddr string, chainID string, consumerId int) {

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
        err = s.seekOldest()
        if err == nil {
                fmt.Println("Started new orderer consumer client for serverAddr="+serverAddr+" chainID="+chainID)
        } else {
                fmt.Println("Received error starting new consumer; err:", err)
        }
        s.umerIdeadUntilClose(consumerId)
}

func executeCmd(cmd string, displayStdout bool) {
        out, err := exec.Command("/bin/sh", "-c", cmd).Output()
        if (err != nil) {
                fmt.Println("unsuccessful exec command: "+cmd+"\nstdout="+out+"\nstderr="+err)
                log.Fatal(err)
        }
}

func executeCmdAndDisplay(cmd string) {
        executeCmd(cmd)
        fmt.Println("results of exec command: "+cmd+"\nstdout="+out)
}
        
func launchnetwork() {
                fmt.Println("Start orderer service, using docker-compose")
                executeCmd("docker-compose up -d")
                fmt.Println("After start orderer service, check containers after sleep 10 secs")
                time.Sleep(10 * time.Second)
		executeCmdAndDisplay("docker ps -a")
}

func startProducer(broadcastAddr string, numTx int64) {

        

        wg.Done()
}


var channelID string = provisional.TestChainID // default hardcoded channel for testing
var channels []string = { channelID }  // later we can enhance code to read/join more channels...
var numOrderers int = 1      // default; the testcase may override this with the number of orderers in the network
var numKBrokers int = 1

var numTxToSend            int64 = 1    // default; the testcase may override this
var numProducers           int   = 1    // default; the testcase may override this
var txSent               []int64        // each producer will count the successfully sent Tx
var txSentFailures       []int64        // each producer will count the send-failures Tx
var totalNumTxSent         int64 = 0
var totalNumTxSentFailures int64 = 0

var numConsumers     int   = 0
var blockRecv      []int64        // each consumer will count the number of blocks successfully delivered (received)
var txRecv         []int64        // each consumer will count the number of Tx successfully delivered (received)
var totalBlockRecv   int64 = 0    // total for all consumers
var totalTxRecv      int64 = 0    // total for all consumers

 //  txRecv[consumerNumber] += len( data )

func main() {

        var serverAddr string

        config := config.Load()
        launchnetwork()

        //var numOrdsToWatch int = numOrderers  // we may want to watch every orderer - when we write tests
                                                // that require stop/restarting the orderers themselves
        var numOrdsToWatch int = 1

        // start threads for a consumer to watch each channel on specified number of orderers.
        // This code assumes orderers in the network will use increasing port numbers:
        // the first ordererer uses default port (7050), the second uses 7051, third uses 7052, etc.
        for ord := 0; ord < numOrdsToWatch; ord++ {
                sprintf(&serverAddr, "%s:%d", config.General.ListenAddress, config.General.ListenPort + ord)
                for c := 0 ; c < len(channels) ; c++ {
                        go startConsumer(serverAddr, channels[c], c)
			numConsumers++
                }
        }

        // now that the orderer service network is running, and the consumers are watching for deliveries,
        // we can start clients which will broadcast the specified number of msgs to their associated orderers
        var wg sync.WaitGroup
        wg.Add(numProducers)
        for producer := 0; producer < numProducers; producer++ {
                sprintf(&serverAddr, "%s:%d", config.General.ListenAddress, config.General.ListenPort + (producer % numOrderers))
                for c := 0 ; c < len(channels) ; c++ {
                        go startProducer(serverAddr, channels[c], numTxToSend/numProducers)
                }
        }

        // Each is done sending.
        // Let's determine if the deliveries have all been received.
        // Wait and recheck as necessary, as long as the delivery counter is getting closer to the broadcast counter

	if !doneConsuming() {    // check if tx counts match on all consumers, or all consumers are no longer receiving blocks
               wait and retry
        }

        computeTotals()
        var successResult bool = false
        if totalTxRecv == numTxToSend (+ genesisblocks for each channel) {
                 // every Tx was successfully sent AND delivered by orderer
                 successResult = true
        }

         // e.g.    totaxTxRecv = sum of txRecv[for all numConsumers]

        if  totalTxRecv == numSent + numSendFailures {
                 // everything that was received by orderer was successfully delivered
        }
        print output result and counts
        return successResult 
}
