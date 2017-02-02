
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

package main

// Orderer Test Engine
// ===================
// Consists of a manager, function ote(), which can:
// + launch a network of orderers per the specified parameters
//   (including kafka brokers or other necessary support processes)
//   by invoking another tool via exec command
// + create producers to send/broadcast msgs to the orderers, concurrently
// + create consumers to invoke deliver on the orderers to receive msgs
// + use parameters for specifying number of channels, number of orderers
// + generate transactions, dividing up the requested TX count among
//   all the channels on the orderers requested, and counts them all
// + confirm all the orderers deliver the same blocks and transactions
// + validate the last block of transactions that was ordered and stored
// + print status results report
// + return a pass/fail result

import (
        "fmt"
        "os"
        "strings"
        "strconv"
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


var producers_wg sync.WaitGroup
var numChannels int = 1
var numOrdsInNtwk  int = 1              // default; the testcase may override this with the number of orderers in the network
var numOrdsToWatch int = 1              // default set to 1; we must watch at least one orderer
var ordererType string = "solo"         // default; the testcase may override this
var numKBrokers int = 0                 // default; the testcase may override this (ignored unless using kafka)
var numConsumers int = 1                // default; this will be set based on other testcase parameters
var numProducers int = 1                // default; this will be set based on other testcase parameters
var numTxToSend int64 = 1               // default; the testcase may override this
                                        // numTxToSend is the total number of Transactions to send;
                                        // A fraction will be sent by each producer - one producer for each channel for each numOrdsInNtwk


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

func (r *ordererdriveClient) readUntilClose(ordererIndex int, channelIndex int, txRecvCntr_p **int64, blockRecvCntr_p **int64) {
        for {
                msg, err := r.client.Recv()
                if err != nil {
                        if !strings.Contains(err.Error(),"transport is closing") {
                                // print if we do not see the msg indicating graceful closing of the connection
                                fmt.Printf("Consumer for orderer %d channel %d readUntilClose() Recv error: %v\n", ordererIndex, channelIndex, err)
                        }
                        return
                }
                switch t := msg.Type.(type) {
                case *ab.DeliverResponse_Status:
                        fmt.Println("Got status ", t)
                        return
                case *ab.DeliverResponse_Block:
                        //fmt.Println("Consumer recvd a block, o c numtrans:", ordererIndex, channelIndex, len(t.Block.Data.Data))
                        **txRecvCntr_p += int64(len(t.Block.Data.Data))
                        //**blockRecvCntr_p = int64(t.Block.Header.Number) // this assumes header number is the block number; instead let's just add one
                        (**blockRecvCntr_p)++
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

func connClose(consumerConns_p_p **([][]*grpc.ClientConn)) {
        for i := 0; i < numOrdsInNtwk; i++ {
                for j := 0; j < numChannels; j++ {
                        _ = (**consumerConns_p_p)[i][j].Close()
                }
        }
}

func startConsumer(serverAddr string, chainID string, ordererIndex int, channelIndex int, txRecvCntr_p *int64, blockRecvCntr_p *int64, consumerConns_p *([][]*grpc.ClientConn)) {
        conn, err := grpc.Dial(serverAddr, grpc.WithInsecure())
        if err != nil {
                fmt.Printf("Error on Consumer ord[%d] ch[%d] connecting (grpc) to %s, err: %v\n", ordererIndex, channelIndex, serverAddr, err)
                return
        }
        (*consumerConns_p)[ordererIndex][channelIndex] = conn
        client, err := ab.NewAtomicBroadcastClient((*consumerConns_p)[ordererIndex][channelIndex]).Deliver(context.TODO())
        if err != nil {
                fmt.Printf("Error on Consumer ord[%d] ch[%d] invoking Deliver() on grpc connection to %s, err: %v\n", ordererIndex, channelIndex, serverAddr, err)
                return
        }
        s := newOrdererdriveClient(client, chainID)
        err = s.seekOldest()
        if err == nil {
                fmt.Printf("Started Consumer to recv delivered batches from ord[%d] ch[%d] srvr=%s chID=%s\n", ordererIndex, channelIndex, serverAddr, chainID)
        } else {
                fmt.Printf("ERROR starting Consumer client for ord[%d] ch[%d] for srvr=%s chID=%s; err: %v\n", ordererIndex, channelIndex, serverAddr, chainID, err)
        }
        s.readUntilClose(ordererIndex, channelIndex, &txRecvCntr_p, &blockRecvCntr_p)
}

func executeCmd(cmd string) []byte {
        out, err := exec.Command("/bin/sh", "-c", cmd).Output()
        if (err != nil) {
                fmt.Println("Unsuccessful exec command: "+cmd+"\nstdout="+string(out)+"\nstderr=", err)
                log.Fatal(err)
        }
        return out
}

func executeCmdAndDisplay(cmd string) {
        out := executeCmd(cmd)
        fmt.Println("Results of exec command: "+cmd+"\nstdout="+string(out))
}

func cleanNetwork(consumerConns_p *([][]*grpc.ClientConn)) {
        //fmt.Println("Removing the Network Consumers")
        connClose(&consumerConns_p)

        // Docker is not perfect; we need to unpause any paused containers, before we can kill them.
        //_ = executeCmd("docker ps -aq -f status=paused | xargs docker unpause")

        // kill any containers that are still running
        //_ = executeCmd("docker kill $(docker ps -q)")

        // remove any running or exited docker processes
        //fmt.Println("Removing the Network orderers and associated docker containers")
        _ = executeCmd("docker rm -f $(docker ps -aq)")
}

func launchNetwork(nOrderers int, nkbs int) {
        fmt.Println("Start orderer service, using docker-compose")
        /*if (nOrderers == 1) {
        _ = executeCmd("docker-compose up -d")
        } else {
        _ = executeCmd("docker-compose -f docker-compose-3orderers.yml up -d")
        }*/
        cmd := fmt.Sprintf("./driver.sh create 1 %d %d level", nOrderers, nkbs)

        //_ = exec.Command("/bin/sh", "-c", cmd).Output()
        executeCmd(cmd)
        executeCmdAndDisplay("docker ps -a")
}

func countGenesis() int64 {
        return int64(numChannels)
}
func sendEqualRecv(numTxToSend int64, totalTxRecv_p *[]int64, totalTxRecvMismatch bool, totalBlockRecvMismatch bool) bool {
                                      // totalTxRecv is a reference only for speed; it will not be changed within
        var matching = false;
        if (*totalTxRecv_p)[0] == numTxToSend {         // recv count on orderer 0 matches the send count
                if !totalTxRecvMismatch && !totalBlockRecvMismatch {  // all orderers have same recv count
                        matching = true
                }
        }
        return matching
}

func moreDeliveries(txSent_p *[][]int64, totalNumTxSent_p *int64, txSentFailures_p *[][]int64, totalNumTxSentFailures_p *int64, txRecv_p *[][]int64, totalTxRecv_p *[]int64, totalTxRecvMismatch_p *bool, blockRecv_p *[][]int64, totalBlockRecv_p *[]int64, totalBlockRecvMismatch_p *bool) (moreReceived bool) {
        moreReceived = false
        prevTotalTxRecv := *totalTxRecv_p
        computeTotals(txSent_p, totalNumTxSent_p, txSentFailures_p, totalNumTxSentFailures_p, txRecv_p, totalTxRecv_p, totalTxRecvMismatch_p, blockRecv_p, totalBlockRecv_p, totalBlockRecvMismatch_p)
        for ordNum := 0; ordNum < numOrdsToWatch; ordNum++ {
                if prevTotalTxRecv[ordNum] != (*totalTxRecv_p)[ordNum] { moreReceived = true }
        }
        return moreReceived
}

func startProducer(serverAddr string, chainID string, ordererIndex int, channelIndex int, txReq int64, txSentCntr_p *int64, txSentFailureCntr_p *int64) {
        conn, err := grpc.Dial(serverAddr, grpc.WithInsecure())
        defer func() {
          _ = conn.Close()
        }()
        if err != nil {
                fmt.Printf("Error creating connection for Producer for ord[%d] ch[%d], err: %v\n", ordererIndex, channelIndex, err)
                return
        }
        client, err := ab.NewAtomicBroadcastClient(conn).Broadcast(context.TODO())
        if err != nil {
                fmt.Printf("Error creating Producer for ord[%d] ch[%d], err: %v\n", ordererIndex, channelIndex, err)
                return
        }
        fmt.Printf("Started Producer to send %d TXs to ord[%d] ch[%d] srvr=%s chID=%s\n", txReq, ordererIndex, channelIndex, serverAddr, chainID)
        b := newBroadcastClient(client, chainID)
        first_err := false
        for i := int64(0); i < txReq ; i++ {
                b.broadcast([]byte(fmt.Sprintf("Testing %v", time.Now())))
                err = b.getAck()
                if err == nil {
                        (*txSentCntr_p)++         // txSent[ordererIndex][channelIndex]++
                } else {
                        (*txSentFailureCntr_p)++
                        if !first_err {
                                first_err = true
                                fmt.Printf("Broadcast error on TX %d (the first error for Producer ord[%d] ch[%d]); err: %v\n", i+1, ordererIndex, channelIndex, err)
                        }
                }
        }
        if err != nil {
                fmt.Printf("Broadcast error on last TX %d of Producer ord[%d] ch[%d]: %v\n", txReq, ordererIndex, channelIndex, err)
        }
        if txReq == *txSentCntr_p {
                fmt.Printf("Producer finished sending broadcast msgs to ord[%d] ch[%d]: ACKs  %9d  (100%%)\n", ordererIndex, channelIndex, *txSentCntr_p)
        } else {
                fmt.Printf("Producer finished sending broadcast msgs to ord[%d] ch[%d]: ACKs  %9d  NACK %d  Other %d\n", ordererIndex, channelIndex, *txSentCntr_p, *txSentFailureCntr_p, txReq - *txSentFailureCntr_p - *txSentCntr_p)
        }
        producers_wg.Done()
}

func computeTotals(txSent *[][]int64, totalNumTxSent *int64, txSentFailures *[][]int64, totalNumTxSentFailures *int64, txRecv *[][]int64, totalTxRecv *[]int64, totalTxRecvMismatch *bool, blockRecv *[][]int64, totalBlockRecv *[]int64, totalBlockRecvMismatch *bool) {
        // Note: txSent and txSentFailures are defined as references for speed, but they are not changed within.

        // Counters for producers are indexed by orderer (numOrdsInNtwk) and channel (numChannels)
        // All counters for all the channels on ALL orderers is the total count.
        // e.g.    totalNumTxSent         = sum of txSent[*][*]
        // e.g.    totalNumTxSentFailures = sum of txSentFailures[*][*]

        *totalNumTxSent = 0
        *totalNumTxSentFailures = 0
        for i := 0; i < numOrdsInNtwk; i++ {
                for j := 0; j < numChannels; j++ {
                        *totalNumTxSent += (*txSent)[i][j]
                        *totalNumTxSentFailures += (*txSentFailures)[i][j]
                }
        }

        // Counters for consumers are indexed by orderer (numOrdsToWatch) and channel (numChannels).
        // All counters for all the channels on JUST ONE orderer is the total count.
        // Tally up the totals for all the channels on each orderer, and store them for comparison; they should all be the same.
        // e.g.    totalTxRecv[k]    = sum of txRecv[k][*]
        // e.g.    totalBlockRecv[k] = sum of blockRecv[k][*]

        *totalTxRecvMismatch = false
        *totalBlockRecvMismatch = false
        for k := 0; k < numOrdsToWatch; k++ {
                (*totalTxRecv)[k] = -countGenesis()    // we are only counting the requested TXs; ignore the genesis block TXs
                (*totalBlockRecv)[k] = -countGenesis() // we are only counting the requested TXs; ignore the genesis blocks
                for l := 0; l < numChannels; l++ {
                        (*totalTxRecv)[k] += (*txRecv)[k][l]
                        (*totalBlockRecv)[k] += (*blockRecv)[k][l]
                        //fmt.Println("in compute(): k, l, txRecv[k][l], blockRecv[k][l] : " , k , l , (*txRecv)[k][l] , (*blockRecv)[k][l] )
                }
                if (k>0) && (*totalTxRecv)[k] != (*totalTxRecv)[k-1] { *totalTxRecvMismatch = true }
                if (k>0) && (*totalBlockRecv)[k] != (*totalBlockRecv)[k-1] { *totalBlockRecvMismatch = true }
        }
        //fmt.Println("in compute(): totalTxRecv[]=" , (*totalTxRecv) , "    totalBlockRecv[]=" , (*totalBlockRecv) )

        // Note: we must remove the orderers (docker containers) to clean up the network; otherwise
        // the totalTxRecv and totalBlockRecv counts would include counts from earlier tests, since
        // ALL the ordered blocks that have accumulated in the orderer will be reported (and
        // not just the ones from this test with this set of producers/consumers).
        // Note: there would be ONLY ONE genesis block for the orderer, for all the tests combined - not one for each test.
}

func reportTotals(numTxToSendTotal int64, countToSend [][]int64, txSent [][]int64, totalNumTxSent int64, txSentFailures [][]int64, totalNumTxSentFailures int64, txRecv [][]int64, totalTxRecv []int64, totalTxRecvMismatch bool, blockRecv [][]int64, totalBlockRecv []int64, totalBlockRecvMismatch bool) (successResult bool, resultStr string) {
        var passFailStr string = "FAILED"
        successResult = false
        resultStr = ""

        // for each producer print the ordererIndex & channel, the TX requested to be sent, the actual num sent and num failed-to-send
        fmt.Println("PRODUCERS  OrdererIndx ChannelIndx   TX Target         ACK        NACK")
        for i := 0; i < numOrdsInNtwk; i++ {
                for j := 0; j < numChannels; j++ {
                        //fmt.Println("PRODUCER for ord",i,"ch",j," TX Requested:",countToSend[i][j]," TX Send ACKs:",txSent[i][j]," TX Send NACKs:",txSentFailures[i][j])
                        fmt.Printf("%22d%12d%12d%12d%12d\n",i,j,countToSend[i][j],txSent[i][j],txSentFailures[i][j])
                }
        }

        // for each consumer print the ordererIndex & channel, the num blocks and the num transactions received/delivered
        fmt.Println("CONSUMERS  OrdererIndx ChannelIndx     Batches         TXs")
        for k := 0; k < numOrdsToWatch; k++ {
                for l := 0; l < numChannels; l++ {
                        //fmt.Println("CONSUMER for ord",k,"ch",l," Blocks:",blockRecv[k][l]," Transactions:",txRecv[k][l])
                        // Subtract one from the received Block count and TX count, to ignore the genesis block
                        // (we already ignore genesis blocks when we compute the totals in totalTxRecv[n] , totalBlockRecv[n])
                        fmt.Printf("%22d%12d%12d%12d\n",k,l,blockRecv[k][l]-1,txRecv[k][l]-1)
                }
        }

        fmt.Printf("Not counting genesis blks (1 per chan)%9d\n", countGenesis())
        fmt.Printf("Total TX broadcasts Requested to Send %9d\n", numTxToSendTotal)
        fmt.Printf("Total TX broadcasts send success ACK  %9d\n", totalNumTxSent)
        fmt.Printf("Total TX broadcasts sendFailed - NACK %9d\n", totalNumTxSentFailures)
        fmt.Printf("Total deliveries Received TX Count    %9d\n", totalTxRecv[0])
        fmt.Printf("Total deliveries Received Blocks      %9d\n", totalBlockRecv[0])
        fmt.Printf("Total LOST transactions               %9d\n", totalNumTxSent + totalNumTxSentFailures - totalTxRecv[0] )

        // Check for differences on the deliveries from the orderers. These are probably errors -
        // unless the test stopped an orderer on purpose and never restarted it, while the
        // others continued to deliver transactions. (If an orderer is restarted, then it
        // would reprocess all the back-ordered transactions to catch up with the others.)

        if totalTxRecvMismatch { fmt.Println("!!!!! Num TXs Delivered is not same on all orderers!!!!!") }
        if totalBlockRecvMismatch { fmt.Println("!!!!! Num Blocks Delivered is not same on all orderers!!!!!") }

        // if totalTxRecv on one orderer == numTxToSendTotal plus a genesisblock for each channel {
        if totalTxRecv[0] == numTxToSendTotal {            // recv count on orderer 0 matches the send count
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
        } else if totalTxRecv[0] == totalNumTxSent + totalNumTxSentFailures {
                if !totalTxRecvMismatch && !totalBlockRecvMismatch {
                        resultStr += "Every ACked TX was delivered, but failures occurred: "
                } else {
                        resultStr += "Orderers were INCONSISTENT: Every ACked TX was delivered, but failures occurred: "
                }
        } else {
                resultStr += "BAD! Some ACKed TX were LOST by orderer service! "
        }

        // print output result and counts : overall summary
        resultStr += fmt.Sprintf("Result=%s: TX Req=%d BrdcstACK=%d NACK=%d DelivBlk=%d DelivTX=%d\n", passFailStr, numTxToSendTotal, totalNumTxSent, totalNumTxSentFailures, totalBlockRecv, totalTxRecv)
        fmt.Printf(resultStr)

        return successResult, resultStr
}

// Function:    ote() - the Orderer Test Engine
// Outputs:     print report to stdout with lots of counters
// Returns:     passed bool, resultSummary string
func ote( txs int64, chans int, orderers int, ordType string, kbs int ) (passed bool, resultSummary string) {
        testformat := fmt.Sprintf("TX=%d Channels=%d Orderers=%d ordererType=%s kafka-brokers=%d", txs, chans, orderers, ordType, kbs)
        fmt.Println("\nIn ote(), args: ", testformat)
        passed = false
        resultSummary = "Test Not Completed: INPUT ERROR: "

        config := config.Load()  // establish the default configuration from yaml files
        ordererType = config.Genesis.OrdererType

        ////////////////////////////////////////////////////////////////////////////////////////////
        // Check parameters and/or env vars to see if user wishes to override default config parms:

        // Arguments to override configuration parameter values in yaml file:

        if ordType != "" {
                ordererType = ordType
        } else {
                fmt.Println("Null value provided for ordererType; using value from config file: ", ordererType)
        }
        if "kafka" == strings.ToLower(ordererType) {
                if kbs > 0 {
                        numKBrokers = kbs
                } else {
                        return passed, resultSummary + "number of kafka-brokers must be > 0"
                }
        } else { numKBrokers = 0 }

        // Arguments for OTE settings for test variations:

        if txs > 0                { numTxToSend = txs   }      else { return passed, resultSummary + "number of transactions must be > 0" }
        if chans > 0              { numChannels = chans }      else { return passed, resultSummary + "number of channels must be > 0" }
        if orderers > 0           { numOrdsInNtwk = orderers } else { return passed, resultSummary + "number of orderers in network must be > 0" }

        // Others, which are dependent on the arguments:
        //
        numProducers = numOrdsInNtwk * numChannels
        numConsumers = numOrdsInNtwk * numChannels
        numOrdsToWatch = numOrdsInNtwk               // watch every orderer to verify they are all delivering the same


        // Each producer sends TXs to one channel on one orderer, and increments its own counters for
        // the successfully sent Tx, and the send-failures (rejected/timeout).
        // These 2D arrays are indexed by dimensions: numOrdsInNtwk and numChannels

        var countToSend        [][]int64       // counter of TX to be sent to each orderer on each channel
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

        var txRecv       [][]int64
        var blockRecv    [][]int64
        var totalTxRecv    []int64          // total TXs received by all consumers on an orderer, indexed by numOrdsToWatch
        var totalBlockRecv []int64          // total Blocks recvd by all consumers on an orderer, indexed by numOrdsToWatch
        var totalTxRecvMismatch bool = false
        var totalBlockRecvMismatch bool = false
        var consumerConns [][]*grpc.ClientConn

        ////////////////////////////////////////////////////////////////////////////////////////////
        // Create the 1D and 2D slices of counters for the producers and consumers. All are initialized to zero.

        for i := 0; i < numOrdsInNtwk; i++ {                    // for all orderers

                countToSendForOrd := make([]int64, numChannels) // create a counter for all the channels on one orderer
                countToSend = append(countToSend, countToSendForOrd) // orderer-i gets a set

                sendPassCntrs := make([]int64, numChannels)     // create a counter for all the channels on one orderer
                txSent = append(txSent, sendPassCntrs)          // orderer-i gets a set

                sendFailCntrs := make([]int64, numChannels)     // create a counter for all the channels on one orderer
                txSentFailures = append(txSentFailures, sendFailCntrs) // orderer-i gets a set

                consumerRow := make([]*grpc.ClientConn, numChannels)
                consumerConns = append(consumerConns, consumerRow)
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

        launchNetwork(orderers, kbs)
        time.Sleep(10 * time.Second)

        ////////////////////////////////////////////////////////////////////////////////////////////
        // Create the 1D slice of channel IDs, and create names for them which we will use
        // when producing/broadcasting/sending msgs and consuming/delivering/receiving msgs.

        var channelIDs []string
        channelIDs = make([]string, numChannels)     // create a slice of channelIDs
        for c:=0; c < numChannels; c++ {
               //channelIDs[c] = fmt.Sprintf("testchan%05d", c)
               // TODO - Since the above statement will not work, just use the hardcoded TestChainID.
               // (We cannot just make up names; instead we must ensure the IDs are the same ones
               // added/created in the launched network itself).
               // And for now we support only one channel.
               // That is all that will make sense numerically, since any consumers for multiple channels
               // on a single orderer would see duplicates since they are arriving with the same TestChainID.
               channelIDs[c] = provisional.TestChainID
               //cmd := fmt.Sprintf("cd ../.. && CORE_PEER_COMMITTER_LEDGER_ORDERER=127.0.0.1:7050 peer channel create -c %s",channelIDs[c])
               //cmd := fmt.Sprintf("cd $GOPATH/src/github.com/hyperledger/fabric && CORE_PEER_COMMITTER_LEDGER_ORDERER=127.0.0.1:5005 peer channel create -c %s",channelIDs[c])
               //executeCmdAndDisplay(cmd)
               //_ = executeCmd(cmd)
        }

        ////////////////////////////////////////////////////////////////////////////////////////////
        // start threads for a consumer to watch each channel on all (the specified number of) orderers.
        // This code assumes orderers in the network will use increasing port numbers:
        // the first ordererer uses default port (7050), the second uses 7051, third uses 7052, etc.

        for ord := 0; ord < numOrdsToWatch; ord++ {
                var ordPort uint16 = 5005
                serverAddr := fmt.Sprintf("%s:%d", config.General.ListenAddress, ordPort + uint16(ord))
                for c := 0 ; c < numChannels ; c++ {
                        go startConsumer(serverAddr, channelIDs[c], ord, c, &(txRecv[ord][c]), &(blockRecv[ord][c]), &consumerConns)
                }
        }

        time.Sleep(5 * time.Second)
        defer cleanNetwork(&consumerConns)

        ////////////////////////////////////////////////////////////////////////////////////////////
        // now that the orderer service network is running, and the consumers are watching for deliveries,
        // we can start clients which will broadcast the specified number of msgs to their associated orderers

        sendStart := time.Now().Unix()
        producers_wg.Add(numProducers)
        for ord := 0; ord < numOrdsInNtwk; ord++ {
                var ordPort uint16 = 5005
                serverAddr := fmt.Sprintf("%s:%d", config.General.ListenAddress, ordPort + uint16(ord))
                for c := 0 ; c < numChannels ; c++ {
                        countToSend[ord][c]= numTxToSend / int64(numProducers)
                        if c==0 && ord==0 { countToSend[ord][c] += numTxToSend % int64(numProducers) }
                        go startProducer(serverAddr, channelIDs[c], ord, c, countToSend[ord][c], &(txSent[ord][c]), &(txSentFailures[ord][c]))
                        time.Sleep(2 * time.Second)
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

        computeTotals(&txSent, &totalNumTxSent, &txSentFailures, &totalNumTxSentFailures, &txRecv, &totalTxRecv, &totalTxRecvMismatch, &blockRecv, &totalBlockRecv, &totalBlockRecvMismatch)
        batchtimeout := 10
        waitSecs := 0
        for !sendEqualRecv(numTxToSend, &totalTxRecv, totalTxRecvMismatch, totalBlockRecvMismatch) && (moreDeliveries(&txSent, &totalNumTxSent, &txSentFailures, &totalNumTxSentFailures, &txRecv, &totalTxRecv, &totalTxRecvMismatch, &blockRecv, &totalBlockRecv, &totalBlockRecvMismatch) || waitSecs <= batchtimeout) { time.Sleep(1 * time.Second); waitSecs++ }

        // Recovery Duration = time spent waiting for orderer service to finish delivering transactions,
        // after all producers finished sending them.
        // waitSecs = some possibly idle time spent waiting for the last batch to be generated (waiting for batchtimeout)
        fmt.Println("Recovery Duration (secs): ", time.Now().Unix() - recoverStart)
        fmt.Println("waitSecs for last batch:  ", waitSecs)
        passed, resultSummary = reportTotals(numTxToSend, countToSend, txSent, totalNumTxSent, txSentFailures, totalNumTxSentFailures, txRecv, totalTxRecv, totalTxRecvMismatch, blockRecv, totalBlockRecv, totalBlockRecvMismatch)

        return passed, resultSummary
}

func main() {
        // Set reasonable defaults in case any env vars are unset.
        var txs int64 = 55
        chans    := 1
        orderers := 1
        ordType  := "kafka"
        kbs      := 3

        // Read env vars
        fmt.Println("\nEnvironment variables provided for this test, and corresponding values actually used for the test:")

        envvar := os.Getenv("OTE_TXS")
        if envvar != "" { txs, _ = strconv.ParseInt(envvar, 10, 64) }
        fmt.Printf("%-40s %s=%d\n", "OTE_TXS="+envvar, "txs", txs)

        envvar = os.Getenv("OTE_CHANNELS")
        if envvar != "" { chans, _ = strconv.Atoi(envvar) }
        fmt.Printf("%-40s %s=%d\n", "OTE_CHANNELS="+envvar, "chans", chans)

        envvar = os.Getenv("OTE_ORDERERS")
        if envvar != "" { orderers, _ = strconv.Atoi(envvar) }
        fmt.Printf("%-40s %s=%d\n", "OTE_ORDERERS="+envvar, "orderers", orderers)

        envvar = os.Getenv("ORDERER_GENESIS_ORDERERTYPE")
        if envvar != "" { ordType = envvar }
        fmt.Printf("%-40s %s=%s\n", "ORDERER_GENESIS_ORDERERTYPE="+envvar, "ordType", ordType)

        envvar = os.Getenv("OTE_KAFKABROKERS")
        if envvar != "" { kbs, _ = strconv.Atoi(envvar) }
        fmt.Printf("%-40s %s=%d\n", "OTE_KAFKABROKERS="+envvar, "kbs", kbs)

        _, _ = ote( txs, chans, orderers, ordType, kbs )
}
