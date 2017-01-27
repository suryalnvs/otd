
/*
Copyright IBM Corp. 2017 All Rights Reserved.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package ote

// Orderer Test Engine
// ===================
// Consists of a manager, function ote(), which can:
// + launch a network of orderers per the specified parameters
//   (including kafka brokers or other necessary support processes)
//   by invoking another tool via exec command
// + create producers to send/broadcast msgs to the orderers, concurrently
// + create consumers to invoke deliver on the orderers to receive msgs
// + use parameters for specifying number of channels, number of orderers
//   to which to broadcast transactions, etc
// + generate transactions, dividing up the requested TX count among
//   all the channels on the orderers requested, and counts them all
// + confirm all the orderers deliver the same blocks and transactions
// + validate the last block of transactions that was ordered and stored
// + print status results report
// + return a pass/fail result

import (
        //"flag"
        "fmt"
        "math"
        "os/exec"
        "log"
        "time"
        "sync"

        "github.com/hyperledger/fabric/orderer/common/bootstrap/provisional"
        "github.com/hyperledger/fabric/orderer/localconfig"
        cb "github.com/hyperledger/fabric/protos/common"
        ab "github.com/hyperledger/fabric/protos/orderer"
        "github.com/hyperledger/fabric/protos/utils"
        "golang.org/x/net/context"
        "github.com/golang/protobuf/proto"
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

func (r *ordererdriveClient) readUntilClose(ordererIndex int, channelIndex int) {
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
                        txRecv[ordererIndex][channelIndex] += int64(len(t.Block.Data.Data))
                        blockRecv[ordererIndex][channelIndex] = int64(t.Block.Header.Number)
                        //fmt.Println("Received block number: ", t.Block.Header.Number, " Transactions of the block: ", len(t.Block.Data.Data), "Total Transactions: ", txRecv[ordererIndex][channelIndex])
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

func startConsumer(serverAddr string, chainID string, ordererIndex int, channelIndex int) {

        conn, err := grpc.Dial(serverAddr, grpc.WithInsecure())
        if err != nil {
                fmt.Println("Error connecting (grpc) to " + serverAddr + ", err: ", err)
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
        s.readUntilClose(ordererIndex, channelIndex)
}

func executeCmd(cmd string) []byte {
        out, err := exec.Command("/bin/sh", "-c", cmd).Output()
        if (err != nil) {
                fmt.Println("unsuccessful exec command: "+cmd+"\nstdout="+string(out)+"\nstderr=", err)
                log.Fatal(err)
        }
        return out
}

func executeCmdAndDisplay(cmd string) {
        out := executeCmd(cmd)
        fmt.Println("results of exec command: "+cmd+"\nstdout="+string(out))
}

func cleanNetwork() {
        fmt.Println("Removing all network nodes docker containers:")
        executeCmdAndDisplay("docker ps -a")

        // Docker is not perfect; we need to unpause any paused containers, before we can kill them.
        //_ = executeCmd("docker ps -aq -f status=paused | xargs docker unpause")

        // kill any containers that are still running
        //_ = executeCmd("docker kill $(docker ps -q)")

        // remove any running or exited docker processes
        _ = executeCmd("docker rm -f $(docker ps -aq)")
}

func launchNetwork() {
        fmt.Println("Start orderer service, using docker-compose")
        _ = executeCmd("docker-compose up -d")
        time.Sleep(2 * time.Second)
        executeCmdAndDisplay("docker ps -a")
}

func countGenesis() int64 {
        return int64(numChannels)
}
func sendEqualRecv() bool {
        var matching = false;
        if (totalTxRecv[0] == numTxToSend + countGenesis()) {            // recv count on orderer 0 matches the send count
                if !totalTxRecvMismatch && !totalBlockRecvMismatch {  // all orderers have same recv count
                        matching = true
                }
        }
        return matching
}

func moreDeliveries() (moreReceived bool) {
        moreReceived = false
        prevTotalTxRecv := totalTxRecv
        computeTotals()
        for ordNum := 0; ordNum < numOrdsToWatch; ordNum++ {
                if prevTotalTxRecv[ordNum] != totalTxRecv[ordNum] { moreReceived = true }
        }
        return moreReceived
}

func startProducer(serverAddr string, chainID string, ordererIndex int, channelIndex int, txReq int64) {
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
        //var counter int64

        for i := int64(0); i < txReq ; i++ {
                b.broadcast([]byte(fmt.Sprintf("Testing %v", time.Now())))
                err = b.getAck()
                if err == nil {
                        txSent [ordererIndex][channelIndex] ++
                } else {
                        txSentFailures [ordererIndex][channelIndex] ++
                }
        }
        if err != nil {
                fmt.Printf("\nError: %v\n", err)
        }
        if txReq == txSent[ordererIndex][channelIndex] {
                fmt.Println(fmt.Sprintf("Total o%dc%d broadcast msg ACKs  %9d", ordererIndex, channelIndex, txSent[ordererIndex][channelIndex]))
        } else {
                fmt.Println(fmt.Sprintf("Total o%dc%d broadcast msg ACKs  %9d", ordererIndex, channelIndex, txSent[ordererIndex][channelIndex]))
                fmt.Println(fmt.Sprintf("Total o%dc%d broadcast msg NACKs %9d", ordererIndex, channelIndex, txSentFailures[ordererIndex][channelIndex]))
                fmt.Println(fmt.Sprintf("Total o%dc%d broadcasts - others %9d", ordererIndex, channelIndex, txReq - txSentFailures[ordererIndex][channelIndex] - txSent[ordererIndex][channelIndex]))
        }
        producers_wg.Done()
}

func computeTotals() {
        // Counters for producers are indexed by orderer (numOrdsToGetTx) and channel (numChannels)
        // All counters for all the channels on ALL orderers is the total count.
        // e.g.    totalNumTxSent         = sum of txSent[*][*]
        // e.g.    totalNumTxSentFailures = sum of txSentFailures[*][*]

        totalNumTxSent = countGenesis()   // one genesis block for each channel always is delivered; start with them, and add the "sent" counters below
        totalNumTxSentFailures = 0
        for i := 0; i < numOrdsToGetTx; i++ {
                for j := 0; j < numChannels; j++ {
                        totalNumTxSent += txSent[i][j]
                        totalNumTxSentFailures += txSentFailures[i][j]
                }
        }

        // Counters for consumers are indexed by orderer (numOrdsToWatch) and channel (numChannels).
        // All counters for all the channels on JUST ONE orderer is the total count.
        // Tally up the totals for all orderers, and store them for comparison; they should all be the same.
        // e.g.    totalTxRecv[k]    = sum of txRecv[k][*]
        // e.g.    totalBlockRecv[k] = sum of blockRecv[k][*]

        totalTxRecvMismatch = false
        totalBlockRecvMismatch = false
        for k := 0; k < numOrdsToWatch; k++ {
                totalTxRecv[k] = 0
                totalBlockRecv[k] = 0
                for l := 0; l < numChannels; l++ {
                        totalTxRecv[k] += txRecv[k][l]
                        totalBlockRecv[k] += blockRecv[k][l]
                }
                if (k>0) && (totalTxRecv[k] != totalTxRecv[k-1]) { totalTxRecvMismatch = true }
                if (k>0) && (totalBlockRecv[k] != totalBlockRecv[k-1]) { totalBlockRecvMismatch = true }
        }
}

func reportTotals() (successResult bool, resultStr string) {

        var passFailStr string = "FAILED"
        successResult = false
        resultStr = ""

        // for each producer print the ordererIndex & channel, the TX requested to be sent, the actual num sent and num failed-to-send
        for i := 0; i < numOrdsToGetTx; i++ {
                for j := 0; j < numChannels; j++ {
                        fmt.Println("PRODUCER for o",i,"c",j," TX Requested:",sendCount[i][j]," TX Send ACKs:",txSent[i][j]," TX Send NACKs:",txSentFailures[i][j])
                }
        }

        // for each consumer print the ordererIndex & channel, the num blocks and the num transactions received/delivered
        for k := 0; k < numOrdsToWatch; k++ {
                for l := 0; l < numChannels; l++ {
                        fmt.Println("CONSUMER for o",k,"c",l," Blocks:",blockRecv[k][l]," Transactions:",txRecv[k][l])
                }
        }

        fmt.Println(fmt.Sprintf("Genesis block TXs (one per channel)   %9d", countGenesis()))
        fmt.Println(fmt.Sprintf("Total TX broadcasts Requested to Send %9d", numTxToSend))
        fmt.Println(fmt.Sprintf("Total TX broadcasts sentSuccessCount  %9d", totalNumTxSent))
        fmt.Println(fmt.Sprintf("Total TX broadcasts sendFailureCount  %9d BAD!", totalNumTxSentFailures))
        fmt.Println(fmt.Sprintf("Total deliveries Received TX Count    %9d", totalTxRecv[0]))
        fmt.Println(fmt.Sprintf("Total deliveries Received Blocks      %9d", totalBlockRecv[0]))
        fmt.Println(fmt.Sprintf("Total LOST transactions               %9d BAD!!!", totalTxRecv[0] - totalNumTxSent - totalNumTxSentFailures))

        // Check for differences on the deliveries from the orderers. These are probably errors -
        // unless the test stopped an orderer on purpose and never restarted it, while the
        // others continued to deliver transactions. (If an orderer is restarted, then it
        // would reprocess all the back-ordered transactions to catch up with the others.)

        if totalTxRecvMismatch { fmt.Println("!!!!! Num TXs Delivered is not same on all orderers!!!!!") }
        if totalBlockRecvMismatch { fmt.Println("!!!!! Num Blocks Delivered is not same on all orderers!!!!!") }

        // if totalTxRecv on one orderer == numTxToSend plus a genesisblock for each channel {
        if totalTxRecv[0] == countGenesis() + numTxToSend {            // recv count on orderer 0 matches the send count
                if !totalTxRecvMismatch && !totalBlockRecvMismatch {
                        // every Tx was successfully sent AND delivered by orderer, and all orderers delivered the same number
                        fmt.Println("Hooray! Every TX was successfully sent AND delivered by orderer service.")
                        successResult = true
                        passFailStr = "PASSED"
                } else {
                        resultStr += "Orderers were INCONSISTENT: "
                        // Every TX was successfully sent AND delivered by at least one orderer -
                        // HOWEVER all orderers that were being watched did not deliver the same counts 
                }
        } else if totalTxRecv[0] == countGenesis() + totalNumTxSent + totalNumTxSentFailures {
                if !totalTxRecvMismatch && !totalBlockRecvMismatch {
                        resultStr += "Every ACked TX was delivered, but NACKs occurred: "
                } else {
                        resultStr += "Orderers were INCONSISTENT: Every ACked TX was delivered, but NACKs occurred: "
                }
        } else {
                resultStr += "BAD! Some ACKed TX were LOST by orderer service! "
        }

        // print output result and counts : overall summary
        resultStr += fmt.Sprint("Result=%s: TX Req=%d BrdcstACK=%d BrdcstNACK=%d DelivBlk=%d DelivTX=%d", passFailStr, numTxToSend, totalNumTxSent, totalNumTxSentFailures, totalBlockRecv, totalTxRecv)
        fmt.Println(resultStr)

        return successResult, resultStr
}

var producers_wg sync.WaitGroup
var channelID string = provisional.TestChainID // default hardcoded channel for testing
//var channels = []string { channelID }   // ...later we can enhance code to read/join more channels...
var channels []string
var numChannels int = 1
var numOrdsInNtwk  int = 1              // default; the testcase may override this with the number of orderers in the network
var numOrdsToWatch int = 1              // default set to 1; we must watch at least one orderer
var numOrdsToGetTx int = 1              // default; the testcase may override this with the number of orderers to recv TXs
var ordererType string = "solo"         // default; the testcase may override this
var numKBrokers int = 0                 // default; the testcase may override this (ignored unless using kafka)
var numConsumers int = 1                // default; this will be set based on other testcase parameters
var numProducers int = 1                // default; this will be set based on other testcase parameters

// numTxToSend is the total number of Transactions to send;
// A fraction will be sent by each producer - one producer for each channel for each numOrdsToGetTx
var numTxToSend            int64 = 1    // default; the testcase may override this

// Each producer sends TXs to one channel on one orderer, and increments its own counters for
// the successfully sent Tx, and the send-failures (rejected/timeout).
// These 2D arrays are indexed by dimensions: numOrdsToGetTx and numChannels

var sendCount          [][]int64       // counter of TX to be sent
var txSent             [][]int64       // TX sendSuccesses on ord[]channel[]
var txSentFailures     [][]int64       // TX sendFailures  on ord[]channel[]
var totalNumTxSent         int64 = 0
var totalNumTxSentFailures int64 = 0

// Each consumer receives blocks delivered on one channel from one orderer,
// and must track its own counters for the received number of blocks and
// received number of Tx.
// We will create consumers for every channel on an orderer, and total up the TXs received.
// And do that for all the orderers (indexed by numOrdsToWatch).
// We will check to ensure all the orderers receive all the same deliveries.
// These 2D arrays are indexed by dimensions: numOrdsToWatch and numChannels

var blockRecv    [][]int64
var txRecv       [][]int64
var totalBlockRecv []int64          // total Blocks recvd by all consumers on an orderer, indexed by numOrdsToWatch
var totalTxRecv    []int64          // total TXs received by all consumers on an orderer, indexed by numOrdsToWatch
var totalTxRecvMismatch bool = false
var totalBlockRecvMismatch bool = false

// return a pass/fail bool, and a result string
func ote( oType string, kbs int, txs int64, oUsed int, oInNtwk int, chans int ) (successResult bool, resultStr string) {

        config := config.Load()  // establish the default configuration from yaml files
        ordererType = config.Genesis.OrdererType

        ////////////////////////////////////////////////////////////////////////////////////////////
        // Check parameters and/or env vars to see if user wishes to override default config parms:

        // Arguments to override configuration parameter values in yaml file:

        if oType != "default"     { ordererType = oType }     // 1- ordererType (solo, kafka, sbft, ...)
        if ordererType == "kafka" { numKBrokers = kbs   }     // 2- num kafka-brokers

        // Arguments for OTE settings for test variations:

        if txs > 0                { numTxToSend = txs   }     // 3- total number of Transactions to send
        if oInNtwk > 0            { numOrdsInNtwk = oInNtwk } // 4- num orderers in network
        if oUsed > 0 && oUsed <= numOrdsInNtwk { numOrdsToGetTx = oUsed } // 5- num orderers to which to send TXs
        if chans > 0              { numChannels = chans }     // 6- num channels to use; Tx will be sent to all channels equally

        // Others, which are dependent on the arguments:
        //
        numProducers = numOrdsToGetTx * numChannels           // determined by (5)x(6)
        numConsumers = numChannels * numOrdsInNtwk            // determined by (6)x(4) - when using all orderers

        numOrdsToWatch = numOrdsInNtwk  // we could assign a value more than one, and watch every orderer -
                                        // to verify they are all delivering the same

        ////////////////////////////////////////////////////////////////////////////////////////////
        // Create the 1D slice of channel IDs, and create names for them which we will use
        // when producing/broadcasting/sending msgs and consuming/delivering/receiving msgs.

        channels = make([]string, numChannels)     // create a counter for each of the channels
        for c:=0; c < numChannels; c++ {
               // channels[c] = fmt.Sprintf("testchan_%05d", c)
               // TODO - Since the above statement will not work, just use the hardcoded TestChainID.
               // (We cannot just make up names; instead we must ensure the IDs are the same ones
               // added/created in the launched network itself).
               // And for now we support only one channel.
               // That is all that will make sense numerically, since any consumers for multiple channels
               // on a single orderer would see duplicates since they are arriving with the same TestChainID.
               channels[c] = provisional.TestChainID
        }

        ////////////////////////////////////////////////////////////////////////////////////////////
        // Create the 1D and 2D slices of counters for the producers and consumers. All are initialized to zero.

        for i := 0; i < numOrdsToGetTx; i++ {  // for all orderers to which we will be sending transactions
                sendPassCntrs := make([]int64, numChannels)     // create a counter for all the channels on one orderer
                txSent = append(txSent, sendPassCntrs)          // orderer-i gets a set
                sendFailCntrs := make([]int64, numChannels)     // create a counter for all the channels on one orderer
                txSentFailures = append(txSentFailures, sendFailCntrs) // orderer-i gets a set
                sendCountsForOrd := make([]int64, numChannels)  // create a counter for all the channels on one orderer
                sendCount = append(sendCount, sendCountsForOrd) // orderer-i gets a set
        }
        for i := 0; i < numOrdsToWatch; i++ {  // for all orderers which we will watch/monitor for deliveries
                blockRecvCntrs := make([]int64, numChannels)  // create a set of block counters for each channel
                blockRecv = append(blockRecv, blockRecvCntrs) // orderer-i gets a set
                txRecvCntrs := make([]int64, numChannels)     // create a set of tx counters for each channel
                txRecv = append(txRecv, txRecvCntrs)          // orderer-i gets a set
        }
        totalTxRecv    = make([]int64, numOrdsToWatch)  // create counter for each orderer, for total tx received (for all channels)
        totalBlockRecv = make([]int64, numOrdsToWatch)  // create counter for each orderer, for total blk received (for all channels)

        ////////////////////////////////////////////////////////////////////////////////////////////
        // For now, launchNetwork() uses docker-compose. later, we will need to pass args to it so it can
        // invoke dongming's script to start a network configuration corresponding to the parameters passed to us by the user
        launchNetwork()

        ////////////////////////////////////////////////////////////////////////////////////////////
        // start threads for a consumer to watch each channel on all (the specified number of) orderers.
        // This code assumes orderers in the network will use increasing port numbers:
        // the first ordererer uses default port (7050), the second uses 7051, third uses 7052, etc.
        for ord := 0; ord < numOrdsToWatch; ord++ {
                serverAddr := fmt.Sprintf("%s:%d", config.General.ListenAddress, config.General.ListenPort + uint16(ord))
                for c := 0 ; c < numChannels ; c++ {
                        go startConsumer(serverAddr, channels[c], ord, c)
                }
        }

        ////////////////////////////////////////////////////////////////////////////////////////////
        // now that the orderer service network is running, and the consumers are watching for deliveries,
        // we can start clients which will broadcast the specified number of msgs to their associated orderers
        sendStart := time.Now().Unix()
        producers_wg.Add(numProducers)
        for ord := 0; ord < numOrdsToGetTx; ord++ {
                serverAddr := fmt.Sprintf("%s:%d", config.General.ListenAddress, config.General.ListenPort + uint16(ord))
                for c := 0 ; c < numChannels ; c++ {
                        sendCount[ord][c]= numTxToSend / int64(numProducers)
                        if c==0 && ord==0 { sendCount[ord][c] += numTxToSend % int64(numProducers) }
                        go startProducer(serverAddr, channels[c], ord, c, sendCount[ord][c])
                }
        }

        producers_wg.Wait()
        fmt.Println("Send Duration (seconds):  ", time.Now().Unix() - sendStart)
        recoverStart := time.Now().Unix()

        ////////////////////////////////////////////////////////////////////////////////////////////
        // All producer threads are finished sending broadcast transactions.
        // Let's determine if the deliveries have all been received by the consumer threads.
        // We will check if the receive counts match the send counts on all consumers, or
        // if all consumers are no longer receiving blocks.
        // Wait and continue rechecking as necessary, as long as the delivery (recv) counters
        // are climbing closer to the broadcast (send) counter.
        computeTotals()
        batchtimeout := 10
        waitSecs := 0
        for !sendEqualRecv() && (moreDeliveries() || waitSecs < batchtimeout) { time.Sleep(1 * time.Second); waitSecs++ }

        fmt.Println("Recovery Duration (secs): ", time.Now().Unix() - recoverStart)
        fmt.Println("Wait Secs", waitSecs)
        fmt.Println("(time waiting for orderer service to finish delivering transactions, after all producers finished sending them)")

        successResult, resultStr = reportTotals()

        //cleanNetwork()

        return successResult, resultStr
}

