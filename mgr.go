package main

// TODO - restructure as a package, change main() to be a function odt(), and then can call odt(with,parms) from testcases

// TODO - restructure for junit output

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
type broadcastClient struct {
	client  ab.AtomicBroadcast_BroadcastClient
	chainID string
}
func newOrdererdriveClient(client ab.AtomicBroadcast_DeliverClient, chainID string) *ordererdriveClient {
        return &ordererdriveClient{client: client, chainID: chainID}
}
func newBroadcastClient(client ab.AtomicBroadcast_BroadcastClient, chainID string) *broadcastClient {
	return &broadcastClient{client: client, chainID: chainID}
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
                }
        }
}

func (b *broadcastClient) broadcast(transaction []byte) error {
	payload, err := proto.Marshal(&cb.Payload{
		Header: &cb.Header{
			ChainHeader: &cb.ChainHeader{
				ChainID: b.chainID,
			},
			SignatureHeader: &cb.SignatureHeader{},
		},
		Data: transaction,
	})
	if err != nil {
		panic(err)
	}
	return b.client.Send(&cb.Envelope{Payload: payload})
}

func (b *broadcastClient) getAck() error {
	msg, err := b.client.Recv()
	if err != nil {
		return err
	}
	if msg.Status != cb.Status_SUCCESS {
		return fmt.Errorf("Got unexpected status: %v", msg.Status)
	}
	return nil
}

func startConsumer(serverAddr string, chainID string, ordererNumber int, consumerNumber int) {

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
        s.readUntilClose(consumerNumber)
}

func executeCmd(cmd string) {
        out, err := exec.Command("/bin/sh", "-c", cmd).Output()
        if (err != nil) {
                fmt.Println("unsuccessful exec command: "+cmd+"\nstdout="+string(out)+"\nstderr="+string(err))
                log.Fatal(err)
        }
}

func executeCmdAndDisplay(cmd string) {
        executeCmd(cmd)
        fmt.Println("results of exec command: "+cmd+"\nstdout="+string(out))
}

func launchnetwork() {
        fmt.Println("Start orderer service, using docker-compose")
        executeCmd("docker-compose up -d")
        fmt.Println("After start orderer service, check containers after sleep 10 secs")
        time.Sleep(10 * time.Second)
	executeCmdAndDisplay("docker ps -a")
}

func startProducer(broadcastAddr string, channelID string, ordererIndex int, channelIndex int, numTx int64) {
     //TODO - Surya
        conn, err := grpc.Dial(serverAddr, grpc.WithInsecure())
        defer func() {
          _ = conn.Close()
        }()
        if err != nil {
           fmt.Println("Error connecting:", err)
           return
        }
        client, err := ab.NewAtomicBroadcastClient(conn).Broadcast(context.TODO())
        if err != nil {
           fmt.Println("Error connecting:", err)
           return
        }

     //return newBroadcastClient(client, chainID)
        b := newBroadcastClient(client, chainID)
        var counter uint64
        counter = 0
        for i := uint64(0); i < messages; i++ {
           b.broadcast([]byte(fmt.Sprintf("Testing %v", time.Now())))
           err = b.getAck()
           if err == nil {
             counter ++
           }
        }
        if err != nil {
           fmt.Printf("\nError: %v\n", err)
        }
        if messages - counter == 0 {
           fmt.Println("Hurray all messages are delivered %d", counter);
        } else {
           fmt.Println("Total Successful messages delivered %d", counter);
           fmt.Println("Messages  that are failed to deliver %d", (messages - counter));
        }
        producers_wg.Done()
}

var producers_wg sync.WaitGroup
var channelID string = provisional.TestChainID // default hardcoded channel for testing
var channels []string = { channelID }   // ...later we can enhance code to read/join more channels...
var numChannels int = len(channels)     // ...later we can enhance code to read/join more channels...
var numOrdsInNtwk  int = 1              // default; the testcase may override this with the number of orderers in the network
var numOrdsToGetTx int = 1              // default; the testcase may override this with the number of orderers to recv TXs
var ordererType string = "solo"         // default; the testcase may override this
var numKBrokers int = 0                 // default; the testcase may override this (ignored unless using kafka)

// numTxToSend is the total number of Transactions to send;
// A fraction will be sent by each producer - one producer for each channel for each numOrdsToGetTx
var numTxToSend            int64 = 1    // default; the testcase may override this

// Each producer sends TXs to one channel on one orderer, and increments its own counters for
// the successfully sent Tx, and the send-failures (rejected/timeout).
var txSent             [][]int64       // indexed dimensions: numOrdsToGetTx and numChannels
var txSentFailures     [][]int64       // indexed dimensions: numOrdsToGetTx and numChannels
var totalNumTxSent         int64 = 0
var totalNumTxSentFailures int64 = 0

// Each consumer receives blocks delivered on one channel from one orderer,
// and must track its own counters for the received number of blocks and
// received number of Tx.
// We will create one consumer for each channel on one orderer, and
// a set of consumers (one for each channel) on ALL the orderers
// (so we can check to ensure they all receive the same deliveries).

var numConsumers     int   = 1
var numProducers     int   = 1
var blockRecv    [][]int64             // indexed dimensions: numOrdsToWatch and numChannels
var txRecv       [][]int64             // indexed dimensions: numOrdsToWatch and numChannels
var totalBlockRecv   int64 = 0         // total for all consumers (on one orderer)
var totalTxRecv      int64 = 0         // total for all consumers (on one orderer)

func main() {

        var serverAddr string

        config := config.Load()  // establish the default configuration from yaml files
        ordererType = config.General.ordererType

	// : Check parameters and/or env vars to see if user wishes to override default config parms:
        // 1- num orderers in network (1, ...)
        // 2- ordererType (solo, kafka, sbft, ...)
        // 3- num kafka-brokers (0, ...) //only makes sense when ordererType==kafka
        // 4- total number of Transactions to send (1, ...)
        // 5- num orderers to which to send transactions (1, ...)   // must be <= (1)
        // 6- num channels to use; Tx will be sent to all channels equally (1, ...)
        // Num producers is determined by (5)x(6)
        // Num consumers is determined by (6) - when using one orderer, or
        //               is determined by (6)x(1) - when using all orderers
        // FUTURE changing Num Channels higher than 1 will require more work to set up...

        // TODO...



        var numOrdsToWatch int = 1
        //numOrdsToWatch = numOrdsInNtwk  // do this if we want to watch every orderer -
                                              // to verify they are all delivering the same
        numConsumers = numOrdsInNtwk * numChannels
        numProducers = numOrdsToGetTx * numChannels

        // Create the 2D arrays of counters

        for i := 0; i < numOrdsToGetTx; i++ {  // for all orderers to which we will be sending transactions
                sendPassCntrs := make([]int64, numChannels) // create a set of counters for each channel
                txSent = append(txSent, sendPassCntrs)      // orderer-i gets a set
                sendFailCntrs := make([]int64, numChannels) // create a set of counters for each channel
                txSentFailures = append(txSentFailures, sendFailCntrs) // orderer-i gets a set
        }
        for i := 0; i < numOrdsToWatch; i++ {  // for all orderers which we will watch/monitor for deliveries
                blockRecvCntrs := make([]int64, numChannels)  // create a set of block counters for each channel
                blockRecv = append(blockRecv, blockRecvCntrs) // orderer-i gets a set
                txRecvCntrs := make([]int64, numChannels)     // create a set of tx counters for each channel
                txRecv = append(txRecv, txRecvCntrs)          // orderer-i gets a set
        }


        // For now, launchnetwork() uses docker-compose. later, we will need to pass args to it so it can
        // invoke dongming's script to start a network configuration corresponding to the parameters passed to us by the user
        launchnetwork()


        // start threads for a consumer to watch each channel on all (the specified number of) orderers.
        // This code assumes orderers in the network will use increasing port numbers:
        // the first ordererer uses default port (7050), the second uses 7051, third uses 7052, etc.
        for ord := 0; ord < numOrdsToWatch; ord++ {
                serverAddr = fmt.Sprintf("%s:%d", config.General.ListenAddress, config.General.ListenPort + ord)
                for c := 0 ; c < numChannels ; c++ {
                        go startConsumer(serverAddr, channels[c], ord, c)
                }
        }

        // now that the orderer service network is running, and the consumers are watching for deliveries,
        // we can start clients which will broadcast the specified number of msgs to their associated orderers
        producers_wg.Add(numProducers)
        for ord := 0; ord < numOrdsToGetTx; ord++ {
                serverAddr = fmt.Sprintf("%s:%d", config.General.ListenAddress, config.General.ListenPort + ord))
                for c := 0 ; c < numChannels ; c++ {
                        sendCount := numTxToSend / numProducers
                        if c==0 { sendCount += numTxToSend % numProducers }
                        go startProducer(serverAddr, channels[c], ord, c, sendCount)
                }
        }

        // Each is done sending.
        // Let's determine if the deliveries have all been received.
        // Wait and recheck as necessary, as long as the delivery counter is getting closer to the broadcast counter

	if !doneConsuming() {    // check if tx counts match on all consumers, or all consumers are no longer receiving blocks
               // TODO - SCOTT
               // loop: wait and retry
        }

        computeTotals()  // TODO - Surya write this function
          // e.g.    totalTxRecv = sum of txRecv[][]     // for all consumers: numOrdsToWatch x numChannels
          // e.g.    totalNumTxSent = sum of txSent[][]  // for all producers: numOrdsToGetTx x numChannels

        var successResult bool = false
        var successStr string = "FAILED"
        // if totalTxRecv == numTxToSend plus a genesisblock for each channel {
        if (totalTxRecv == numTxToSend + numChannels) {
                 // every Tx was successfully sent AND delivered by orderer
                 fmt.Println("Hooray! Every TX was successfully sent AND delivered by orderer service")
                 successResult = true
                 successStr = "PASSED"
        }

        else if (totalTxRecv == totalNumTxSent + totalNumTxSentFailures) {
                 fmt.Println("Good (but not perfect)! Every TX that was acknowledged by orderer service was also successfully delivered")
        }

        else {
                 fmt.Println("BOO! Some acknowledged TX were LOST by orderer service!")
        }


        /////////////////// TODO - Surya     MOVE THIS INTO A FUNCTION /////////////////////////
        //

        // print output result and counts
        fmt.Println(fmt.Sprintf("Testname %s %s, TX Req=%d SendSuccess=%d SendFail=%d DelivBlock=%d DelivTX=%d", testName, successStr, numTxToSend, totalNumTxSent, totalNumTxSentFailures, totalBlockRecv, totalTxRecv))

        // for each producer print the ordererNumber & channel, the TX requested to be sent, the actual num sent and num failed-to-send

        // for each consumer print the ordererNumber & channel, the num blocks and the num transactions received/delivered

        //
        /////////////////// MOVE THIS INTO A FUNCTION /////////////////////////



        return successResult
}
