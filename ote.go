
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
var ordStartPort uint16 = 5005          // starting port used by the tool called by launchNetwork()
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

var debugflag1 bool = false
var logEnabled bool
var logFile *os.File

func InitLogger(fileName string) {
        layout := "Jan_02_2006"
        // Format Now with the layout const.
        t := time.Now()
        res := t.Format(layout)
        var err error
        logFile, err = os.OpenFile(fileName+"-"+res+".log", os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)
        if err != nil {
                panic(fmt.Sprintf("error opening file: %s", err))
        }
        logEnabled = true
        log.SetOutput(logFile)
        //log.SetFlags(log.LstdFlags | log.Lshortfile)
        log.SetFlags(log.LstdFlags)
}

func Logger(printStmt string) {
        fmt.Println(printStmt)
        if !logEnabled {
                return
        }
        //TODO: Should we disable logging ?
        log.Println(printStmt)
}

func CloseLogger() {
        if logEnabled && logFile != nil {
                logFile.Close()
        }
}

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

func (r *ordererdriveClient) readUntilClose(ordererIndex int, channelIndex int, txRecvCntr_p *int64, blockRecvCntr_p *int64) {
        for {
                msg, err := r.client.Recv()
                if err != nil {
                        if !strings.Contains(err.Error(),"transport is closing") {
                                // print if we do not see the msg indicating graceful closing of the connection
                                Logger(fmt.Sprintf("Consumer for orderer %d channel %d readUntilClose() Recv error: %v", ordererIndex, channelIndex, err))
                        }
                        return
                }
                switch t := msg.Type.(type) {
                case *ab.DeliverResponse_Status:
                        Logger(fmt.Sprintf("Got DeliverResponse_Status: %v", t))
                        return
                case *ab.DeliverResponse_Block:
                        if t.Block.Header.Number > 0 {
                                if debugflag1 { Logger(fmt.Sprintf("Consumer recvd a block, o %d c %d blkNum %d numtrans %d", ordererIndex, channelIndex, t.Block.Header.Number, len(t.Block.Data.Data))) }
                                // if debugflag1 { Logger(fmt.Sprintf("blk: %v", t.Block.Data.Data)) }
                        }
                        *txRecvCntr_p += int64(len(t.Block.Data.Data))
                        //*blockRecvCntr_p = int64(t.Block.Header.Number) // this assumes header number is the block number; instead let's just add one
                        (*blockRecvCntr_p)++
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

func startConsumer(serverAddr string, chainID string, ordererIndex int, channelIndex int, txRecvCntr_p *int64, blockRecvCntr_p *int64, consumerConn_p **grpc.ClientConn) {
        conn, err := grpc.Dial(serverAddr, grpc.WithInsecure())
        if err != nil {
                Logger(fmt.Sprintf("Error on Consumer ord[%d] ch[%d] connecting (grpc) to %s, err: %v", ordererIndex, channelIndex, serverAddr, err))
                return
        }
        (*consumerConn_p) = conn
        client, err := ab.NewAtomicBroadcastClient(*consumerConn_p).Deliver(context.TODO())
        if err != nil {
                Logger(fmt.Sprintf("Error on Consumer ord[%d] ch[%d] invoking Deliver() on grpc connection to %s, err: %v", ordererIndex, channelIndex, serverAddr, err))
                return
        }
        s := newOrdererdriveClient(client, chainID)
        err = s.seekOldest()
        if err == nil {
                if debugflag1 { Logger(fmt.Sprintf("Started Consumer to recv delivered batches from ord[%d] ch[%d] srvr=%s chID=%s", ordererIndex, channelIndex, serverAddr, chainID)) }
        } else {
                Logger(fmt.Sprintf("ERROR starting Consumer client for ord[%d] ch[%d] for srvr=%s chID=%s; err: %v", ordererIndex, channelIndex, serverAddr, chainID, err))
        }
        s.readUntilClose(ordererIndex, channelIndex, txRecvCntr_p, blockRecvCntr_p)
}

func startConsumerMaster(serverAddr string, chainIDs_p *[]string, ordererIndex int, txRecvCntrs_p *[]int64, blockRecvCntrs_p *[]int64, consumerConn_p **grpc.ClientConn) {
        // create one conn to the orderer and share it for communications to all channels
        conn, err := grpc.Dial(serverAddr, grpc.WithInsecure())
        if err != nil {
                Logger(fmt.Sprintf("Error on MasterConsumer ord[%d] connecting (grpc) to %s, err: %v", ordererIndex, serverAddr, err))
                return
        }
        (*consumerConn_p) = conn

        // create an orderer driver client for every channel on this orderer
        //[][]*ordererdriveClient  //  numChannels
        dc := make ([]*ordererdriveClient, numChannels)
        for c := 0; c < numChannels; c++ {
                client, err := ab.NewAtomicBroadcastClient(*consumerConn_p).Deliver(context.TODO())
                if err != nil {
                        Logger(fmt.Sprintf("Error on MasterConsumer ord[%d] invoking Deliver() on grpc connection to %s, err: %v", ordererIndex, serverAddr, err))
                        return
                }
                dc[c] = newOrdererdriveClient(client, (*chainIDs_p)[c])
                err = dc[c].seekOldest()
                if err == nil {
                        if debugflag1 { Logger(fmt.Sprintf("Started MasterConsumer to recv delivered batches from ord[%d] ch[%d] srvr=%s chID=%s", ordererIndex, c, serverAddr, (*chainIDs_p)[c])) }
                } else {
                        Logger(fmt.Sprintf("ERROR starting MasterConsumer client for ord[%d] ch[%d] for srvr=%s chID=%s; err: %v", ordererIndex, c, serverAddr, (*chainIDs_p)[c], err))
                }
                // we would prefer to skip these go threads, and just have on "readUntilClose" that looks for deliveries on all channels!!! (see below.)
                // otherwise, what have we really saved?
                go dc[c].readUntilClose(ordererIndex, c, &((*txRecvCntrs_p)[c]), &((*blockRecvCntrs_p)[c]))
        }
}

func executeCmd(cmd string) []byte {
        out, err := exec.Command("/bin/sh", "-c", cmd).Output()
        if (err != nil) {
                Logger(fmt.Sprintf("Unsuccessful exec command: "+cmd+"\nstdout="+string(out)+"\nstderr=%v", err))
                log.Fatal(err)
        }
        return out
}

func executeCmdAndDisplay(cmd string) {
        out := executeCmd(cmd)
        Logger("Results of exec command: "+cmd+"\nstdout="+string(out))
}

func connClose(consumerConns_p_p **([][]*grpc.ClientConn)) {
        for i := 0; i < numOrdsToWatch; i++ {
                for j := 0; j < numChannels; j++ {
                        if (**consumerConns_p_p)[i][j] != nil {
                                _ = (**consumerConns_p_p)[i][j].Close()
                        }
                }
        }
}

func cleanNetwork(consumerConns_p *([][]*grpc.ClientConn)) {
        if debugflag1 { Logger("Removing the Network Consumers") }
        connClose(&consumerConns_p)

        // Docker is not perfect; we need to unpause any paused containers, before we can kill them.
        //_ = executeCmd("docker ps -aq -f status=paused | xargs docker unpause")

        // kill any containers that are still running
        //_ = executeCmd("docker kill $(docker ps -q)")

        if debugflag1 { Logger("Removing the Network orderers and associated docker containers") }
        _ = executeCmd("docker rm -f $(docker ps -aq)")
}

func launchNetwork(nOrderers int, nkbs int) {
        Logger("Start orderer service, using docker-compose")
        /*
        if (nOrderers == 1) {
          _ = executeCmd("docker-compose up -d")
        } else {
          _ = executeCmd("docker-compose -f docker-compose-3orderers.yml up -d")
        }
        */
        cmd := fmt.Sprintf("./driver.sh create 1 %d %d level", nOrderers, nkbs)
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
                Logger(fmt.Sprintf("Error creating connection for Producer for ord[%d] ch[%d], err: %v", ordererIndex, channelIndex, err))
                return
        }
        client, err := ab.NewAtomicBroadcastClient(conn).Broadcast(context.TODO())
        if err != nil {
                Logger(fmt.Sprintf("Error creating Producer for ord[%d] ch[%d], err: %v", ordererIndex, channelIndex, err))
                return
        }
        if debugflag1 { Logger(fmt.Sprintf("Started Producer to send %d TXs to ord[%d] ch[%d] srvr=%s chID=%s", txReq, ordererIndex, channelIndex, serverAddr, chainID)) }
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
                                Logger(fmt.Sprintf("Broadcast error on TX %d (the first error for Producer ord[%d] ch[%d]); err: %v", i+1, ordererIndex, channelIndex, err))
                        }
                }
        }
        if err != nil {
                Logger(fmt.Sprintf("Broadcast error on last TX %d of Producer ord[%d] ch[%d]: %v", txReq, ordererIndex, channelIndex, err))
        }
        if txReq == *txSentCntr_p {
                if debugflag1 { Logger(fmt.Sprintf("Producer finished sending broadcast msgs to ord[%d] ch[%d]: ACKs  %9d  (100%%)", ordererIndex, channelIndex, *txSentCntr_p)) }
        } else {
                Logger(fmt.Sprintf("Producer finished sending broadcast msgs to ord[%d] ch[%d]: ACKs  %9d  NACK %d  Other %d", ordererIndex, channelIndex, *txSentCntr_p, *txSentFailureCntr_p, txReq - *txSentFailureCntr_p - *txSentCntr_p))
        }
        producers_wg.Done()
}

func startProducerMaster(serverAddr string, chainIDs *[]string, ordererIndex int, txReq_p *[]int64, txSentCntr_p *[]int64, txSentFailureCntr_p *[]int64) {
        // This function creates a grpc connection to one orderer,
        // creates multiple clients (one per numChannels) for that one orderer,
        // and sends a TX to all channels repeatedly until no more to send.
        var txReqTotal int64 = 0
        var txMax int64 = 0
        for c := 0; c < numChannels; c++ {
                txReqTotal += (*txReq_p)[c]
                if txMax < (*txReq_p)[c] { txMax = (*txReq_p)[c] }
        }
        conn, err := grpc.Dial(serverAddr, grpc.WithInsecure())
        defer func() {
          _ = conn.Close()
        }()
        if err != nil {
                Logger(fmt.Sprintf("Error creating connection for MasterProducer for ord[%d], err: %v", ordererIndex, err))
                return
        }
        client, err := ab.NewAtomicBroadcastClient(conn).Broadcast(context.TODO())
        if err != nil {
                Logger(fmt.Sprintf("Error creating MasterProducer for ord[%d], err: %v", ordererIndex, err))
                return
        }
        Logger(fmt.Sprintf("Started MasterProducer to send %d TXs to ord[%d] srvr=%s distributed across all channels", txReqTotal, ordererIndex, serverAddr))

        // create the broadcast clients for every channel on this orderer
        bc := make ([]*broadcastClient, numChannels)
        for c := 0; c < numChannels; c++ {
                bc[c] = newBroadcastClient(client, (*chainIDs)[c])
        }

        first_err := false
        for i := int64(0); i < txMax; i++ {
                // send one TX to every broadcast client (i.e. one TX on each channel)
                for c := 0; c < numChannels; c++ {
                        if i < (*txReq_p)[c] { // if more TXs to send on this channel
                                bc[c].broadcast([]byte(fmt.Sprintf("Testing %v", time.Now())))
                                err = bc[c].getAck()
                                if err == nil {
                                        (*txSentCntr_p)[c]++         // txSent[ordererIndex][channelIndex]++
                                } else {
                                        (*txSentFailureCntr_p)[c]++
                                        if !first_err {
                                                first_err = true
                                                Logger(fmt.Sprintf("Broadcast error on TX %d (the first error for MasterProducer on ord[%d] ch[%d] channelID=%s); err: %v", i+1, ordererIndex, c, (*chainIDs)[c], err))
                                        }
                                }
                        }
                }
        }
        if err != nil {
                Logger(fmt.Sprintf("Broadcast error on last TX %d of MasterProducer on ord[%d] ch[%d]: %v", txReqTotal, ordererIndex, numChannels-1, err))
        }
        var txSentTotal int64 = 0
        var txSentFailTotal int64 = 0
        for c := 0; c < numChannels; c++ {
                txSentTotal += (*txSentCntr_p)[c]
                txSentFailTotal += (*txSentFailureCntr_p)[c]
        }
        if txReqTotal == txSentTotal {
                Logger(fmt.Sprintf("MasterProducer finished sending broadcast msgs to all channels on ord[%d]: ACKs  %9d  (100%%)", ordererIndex, txSentTotal))
        } else {
                Logger(fmt.Sprintf("MasterProducer finished sending broadcast msgs to all channels on ord[%d]: ACKs  %9d  NACK %d  Other %d", ordererIndex, txSentTotal, txSentFailTotal, txReqTotal - txSentTotal - txSentFailTotal))
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
                        if debugflag1 { Logger(fmt.Sprintf("in compute(): k %d l %d txRecv[k][l] %d blockRecv[k][l] %d", k , l , (*txRecv)[k][l] , (*blockRecv)[k][l] )) }
                }
                if (k>0) && (*totalTxRecv)[k] != (*totalTxRecv)[k-1] { *totalTxRecvMismatch = true }
                if (k>0) && (*totalBlockRecv)[k] != (*totalBlockRecv)[k-1] { *totalBlockRecvMismatch = true }
        }
        if debugflag1 { Logger(fmt.Sprintf("in compute(): totalTxRecv[]= %v, totalBlockRecv[]= %v", *totalTxRecv, *totalBlockRecv)) }

        // Note: we must remove the orderers (docker containers) to clean up the network; otherwise
        // the totalTxRecv and totalBlockRecv counts would include counts from earlier tests, since
        // ALL the ordered blocks that have accumulated in the orderer will be delivered and reported
        // (and not just the ones from this test with this set of producers/consumers).
        // Also note, in that case, there would be ONLY ONE genesis block for the orderer, for
        // all the tests combined - not one for each test.
}

func reportTotals(numTxToSendTotal int64, countToSend [][]int64, txSent [][]int64, totalNumTxSent int64, txSentFailures [][]int64, totalNumTxSentFailures int64, txRecv [][]int64, totalTxRecv []int64, totalTxRecvMismatch bool, blockRecv [][]int64, totalBlockRecv []int64, totalBlockRecvMismatch bool, masterSpy bool) (successResult bool, resultStr string) {
        var passFailStr string = "FAILED"
        successResult = false
        resultStr = ""

        // for each producer print the ordererIndex & channel, the TX requested to be sent, the actual num sent and num failed-to-send
        Logger(fmt.Sprintf("Print only the first 3 chans of only the first 3 ordererIdx; and any others ONLY IF they contain failures.\nTotals numOrdInNtwk=%d numChan=%d numPRODUCERs=%d", numOrdsInNtwk, numChannels, numOrdsInNtwk*numChannels))
        Logger("PRODUCERS   OrdererIdx  ChannelIdx   TX Target         ACK        NACK")
        for i := 0; i < numOrdsInNtwk; i++ {
                for j := 0; j < numChannels; j++ {
                        if (i < 3 && j < 3) || txSentFailures[i][j] > 0 || countToSend[i][j] != txSent[i][j] + txSentFailures[i][j] {
                                Logger(fmt.Sprintf("%22d%12d%12d%12d%12d",i,j,countToSend[i][j],txSent[i][j],txSentFailures[i][j]))
                        } else if (i < 3 && j == 3) {
                                Logger(fmt.Sprintf("%34s","..."))
                        } else if (i == 3 && j == 0) {
                                Logger(fmt.Sprintf("%22s","..."))
                        }
                }
        }

        // for each consumer print the ordererIndex & channel, the num blocks and the num transactions received/delivered
        Logger(fmt.Sprintf("Print only the first 3 chans of only the first 3 ordererIdx (and the last ordererIdx if masterSpy is present), plus any others that look wrong.\nTotals numOrdIdx=%d numChanIdx=%d numCONSUMERS=%d", numOrdsToWatch, numChannels, numOrdsToWatch*numChannels))
        Logger("CONSUMERS   OrdererIdx  ChannelIdx     Batches         TXs")
        for i := 0; i < numOrdsToWatch; i++ {
                for j := 0; j < numChannels; j++ {
                        if (j < 3 && (i < 3 || (masterSpy && i==numOrdsInNtwk-1))) || (i>1 && (blockRecv[i][j] != blockRecv[1][j] || txRecv[1][j] != txRecv[1][j])) {
                                // Subtract one from the received Block count and TX count, to ignore the genesis block
                                // (we already ignore genesis blocks when we compute the totals in totalTxRecv[n] , totalBlockRecv[n])
                                Logger(fmt.Sprintf("%22d%12d%12d%12d",i,j,blockRecv[i][j]-1,txRecv[i][j]-1))
                        } else if (i < 3 && j == 3) {
                                Logger(fmt.Sprintf("%34s","..."))
                        } else if (i == 3 && j == 0) {
                                Logger(fmt.Sprintf("%22s","..."))
                        }
                }
        }

        Logger(fmt.Sprintf("Not counting genesis blks (1 per chan)%9d", countGenesis()))
        Logger(fmt.Sprintf("Total TX broadcasts Requested to Send %9d", numTxToSendTotal))
        Logger(fmt.Sprintf("Total TX broadcasts send success ACK  %9d", totalNumTxSent))
        Logger(fmt.Sprintf("Total TX broadcasts sendFailed - NACK %9d", totalNumTxSentFailures))
        Logger(fmt.Sprintf("Total deliveries Received TX Count    %9d", totalTxRecv[0]))
        Logger(fmt.Sprintf("Total deliveries Received Blocks      %9d", totalBlockRecv[0]))
        Logger(fmt.Sprintf("Total LOST transactions               %9d", totalNumTxSent + totalNumTxSentFailures - totalTxRecv[0] ))

        // Check for differences on the deliveries from the orderers. These are probably errors -
        // unless the test stopped an orderer on purpose and never restarted it, while the
        // others continued to deliver transactions. (If an orderer is restarted, then it
        // would reprocess all the back-ordered transactions to catch up with the others.)

        if totalTxRecvMismatch { Logger("!!!!! Num TXs Delivered is not same on all orderers!!!!!") }
        if totalBlockRecvMismatch { Logger("!!!!! Num Blocks Delivered is not same on all orderers!!!!!") }

        // if totalTxRecv on one orderer == numTxToSendTotal plus a genesisblock for each channel {
        if totalTxRecv[0] == numTxToSendTotal {            // recv count on orderer 0 matches the send count
                if !totalTxRecvMismatch && !totalBlockRecvMismatch {
                        // every Tx was successfully sent AND delivered by orderer, and all orderers delivered the same number
                        Logger("Hooray! Every TX was successfully sent AND delivered by orderer service.")
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
        resultStr += fmt.Sprintf("Result=%s: TX Req=%d BrdcstACK=%d NACK=%d DelivBlk=%d DelivTX=%d", passFailStr, numTxToSendTotal, totalNumTxSent, totalNumTxSentFailures, totalBlockRecv, totalTxRecv)
        Logger(fmt.Sprintf(resultStr))

        return successResult, resultStr
}

// Function:    ote() - the Orderer Test Engine
// Outputs:     print report to stdout with lots of counters
// Returns:     passed bool, resultSummary string
func ote( txs int64, chans int, orderers int, ordType string, kbs int, optimizeClientsMode bool, masterSpy bool ) (passed bool, resultSummary string) {
        Logger(fmt.Sprintf("TX=%d Channels=%d Orderers=%d ordererType=%s kafka-brokers=%d optimizeClients=%t addMasterSpy=%t", txs, chans, orderers, ordType, kbs, optimizeClientsMode, masterSpy))
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
                Logger(fmt.Sprintf("Null value provided for ordererType; using value from config file: %s", ordererType))
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
        numOrdsToWatch = numOrdsInNtwk    // Watch every orderer to verify they are all delivering the same.
        if masterSpy { numOrdsToWatch++ } // We are not creating another orderer here, but we do need
                                          // another set of counters; the masterSpy will be created for
                                          // this test to watch every channel on an orderer - so that means
                                          // one orderer is being watched twice
        if optimizeClientsMode {
                // use only one MasterProducer and one MasterConsumer on each orderer
                numProducers = numOrdsInNtwk
                numConsumers = numOrdsInNtwk
        } else {
                // one Producer and one Consumer for EVERY channel on each orderer
                numProducers = numOrdsInNtwk * numChannels
                numConsumers = numOrdsInNtwk * numChannels
        }


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
        }

        for i := 0; i < numOrdsToWatch; i++ {  // for all orderers which we will watch/monitor for deliveries

                blockRecvCntrs := make([]int64, numChannels)  // create a set of block counters for each channel
                blockRecv = append(blockRecv, blockRecvCntrs) // orderer-i gets a set

                txRecvCntrs := make([]int64, numChannels)     // create a set of tx counters for each channel
                txRecv = append(txRecv, txRecvCntrs)          // orderer-i gets a set

                consumerRow := make([]*grpc.ClientConn, numChannels)
                consumerConns = append(consumerConns, consumerRow)
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
        // TODO (after FAB-2001 is fixed) - Remove the if-then clause.
        // Due to FAB-2001 bug, we cannot test with multiple orderers and multiple channels.
        // Currently it will work only with single orderer and multiple channels.
        // TEMPORARY PARTIAL SOLUTION: To test multiple orderers with a single channel,
        // use hardcoded TestChainID and skip creating any channels.
      if numChannels == 1 && numOrdsInNtwk > 1 {
              channelIDs[0] = provisional.TestChainID
              Logger(fmt.Sprintf("Using DEFAULT channelID = %s", channelIDs[0]))
      } else {
        Logger(fmt.Sprintf("Using %d new channelIDs, e.g. testchan00023", numChannels))
        for c:=0; c < numChannels; c++ {
                channelIDs[c] = fmt.Sprintf("testchan%05d", c)
                cmd := fmt.Sprintf("cd $GOPATH/src/github.com/hyperledger/fabric && CORE_PEER_COMMITTER_LEDGER_ORDERER=127.0.0.1:%d peer channel create -c %s", ordStartPort, channelIDs[c])
                _ = executeCmd(cmd)
                //executeCmdAndDisplay(cmd)
        }
      }

        ////////////////////////////////////////////////////////////////////////////////////////////
        // start threads for a consumer to watch each channel on all (the specified number of) orderers.
        // This code assumes orderers in the network will use increasing port numbers:
        // the first ordererer uses default port (7050), the second uses default+1(7051), etc.

        for ord := 0; ord < numOrdsToWatch; ord++ {
                serverAddr := fmt.Sprintf("%s:%d", config.General.ListenAddress, ordStartPort + uint16(ord))
                if masterSpy && ord == numOrdsToWatch-1 {
                        // Special case: this is the last row of counters, added (and incremented numOrdsToWatch) for the masterSpy
                        // to use to watch the first orderer for deliveries, on all channels. This will be a duplicate Consumer
                        // (it is the second one monitoring the first orderer), so we need to reuse the first port.
                        serverAddr = fmt.Sprintf("%s:%d", config.General.ListenAddress, ordStartPort)
                        go startConsumerMaster(serverAddr, &channelIDs, ord, &(txRecv[ord]), &(blockRecv[ord]), &(consumerConns[ord][0]))
                } else
                if optimizeClientsMode {
                        // create just one Consumer to receive all deliveries (on all channels) on an orderer
                        go startConsumerMaster(serverAddr, &channelIDs, ord, &(txRecv[ord]), &(blockRecv[ord]), &(consumerConns[ord][0]))
                } else {
                        // normal mode: create a unique consumer client go thread for each channel on each orderer
                        for c := 0 ; c < numChannels ; c++ {
                                go startConsumer(serverAddr, channelIDs[c], ord, c, &(txRecv[ord][c]), &(blockRecv[ord][c]), &(consumerConns[ord][c]))
                        }
                }

        }

        Logger("Finished creating all CONSUMERS clients")
        time.Sleep(5 * time.Second)
        defer cleanNetwork(&consumerConns)

        ////////////////////////////////////////////////////////////////////////////////////////////
        // now that the orderer service network is running, and the consumers are watching for deliveries,
        // we can start clients which will broadcast the specified number of msgs to their associated orderers

        if optimizeClientsMode {
                producers_wg.Add(numOrdsInNtwk)
        } else {
                producers_wg.Add(numProducers)
        }
        sendStart := time.Now().Unix()
        for ord := 0; ord < numOrdsInNtwk; ord++ {    // on each orderer:
                serverAddr := fmt.Sprintf("%s:%d", config.General.ListenAddress, ordStartPort + uint16(ord))
                for c := 0 ; c < numChannels ; c++ {
                        countToSend[ord][c] = numTxToSend / int64(numOrdsInNtwk * numChannels)
                        if c==0 && ord==0 { countToSend[ord][c] += numTxToSend % int64(numOrdsInNtwk * numChannels) }
                }
                if optimizeClientsMode {
                        // create one Producer for all channels on this orderer
                        go startProducerMaster(serverAddr, &channelIDs, ord, &(countToSend[ord]), &(txSent[ord]), &(txSentFailures[ord]))
                } else {
                        // normal mode: create a unique consumer client go thread for each channel
                        for c := 0 ; c < numChannels ; c++ {
                                go startProducer(serverAddr, channelIDs[c], ord, c, countToSend[ord][c], &(txSent[ord][c]), &(txSentFailures[ord][c]))
                        }
                }
        }

        if optimizeClientsMode {
                Logger(fmt.Sprintf("Finished creating all %d MASTER-PRODUCERs", numOrdsInNtwk))
        } else {
                Logger(fmt.Sprintf("Finished creating all %d PRODUCERs", numOrdsInNtwk * numChannels))
        }
        producers_wg.Wait()
        Logger(fmt.Sprintf("Send Duration (seconds): %4d", time.Now().Unix() - sendStart))
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
        Logger(fmt.Sprintf("Recovery Duration (secs):%4d", time.Now().Unix() - recoverStart))
        Logger(fmt.Sprintf("waitSecs for last batch: %4d", waitSecs))
        passed, resultSummary = reportTotals(numTxToSend, countToSend, txSent, totalNumTxSent, txSentFailures, totalNumTxSentFailures, txRecv, totalTxRecv, totalTxRecvMismatch, blockRecv, totalBlockRecv, totalBlockRecvMismatch, masterSpy)

        return passed, resultSummary
}

func main() {

        InitLogger("ote")

        // Set reasonable defaults in case any env vars are unset.
        var txs int64 = 55
        chans    := 1
        orderers := 1
        ordType  := "kafka"
        kbs      := 3
        optimizeClients := false
        addMasterSpy := false

        // Read env vars
        Logger("\nEnvironment variables provided for this test, and corresponding values actually used for the test:")

        envvar := os.Getenv("OTE_TXS")
        if envvar != "" { txs, _ = strconv.ParseInt(envvar, 10, 64) }
        Logger(fmt.Sprintf("%-40s %s=%d", "OTE_TXS="+envvar, "txs", txs))

        envvar = os.Getenv("OTE_CHANNELS")
        if envvar != "" { chans, _ = strconv.Atoi(envvar) }
        Logger(fmt.Sprintf("%-40s %s=%d", "OTE_CHANNELS="+envvar, "chans", chans))

        envvar = os.Getenv("OTE_ORDERERS")
        if envvar != "" { orderers, _ = strconv.Atoi(envvar) }
        Logger(fmt.Sprintf("%-40s %s=%d", "OTE_ORDERERS="+envvar, "orderers", orderers))

        envvar = os.Getenv("ORDERER_GENESIS_ORDERERTYPE")
        if envvar != "" { ordType = envvar }
        Logger(fmt.Sprintf("%-40s %s=%s", "ORDERER_GENESIS_ORDERERTYPE="+envvar, "ordType", ordType))

        envvar = os.Getenv("OTE_KAFKABROKERS")
        if envvar != "" { kbs, _ = strconv.Atoi(envvar) }
        Logger(fmt.Sprintf("%-40s %s=%d", "OTE_KAFKABROKERS="+envvar, "kbs", kbs))

        envvar = os.Getenv("OTE_OPTIMIZE_CLIENTS")
        if "true" == strings.ToLower(envvar) || "t" == strings.ToLower(envvar) { optimizeClients = true }
        Logger(fmt.Sprintf("%-40s %s=%t", "OTE_OPTIMIZE_CLIENTS="+envvar, "optimizeClients", optimizeClients))

        envvar = os.Getenv("OTE_MASTERSPY")
        if "true" == strings.ToLower(envvar) || "t" == strings.ToLower(envvar) { addMasterSpy = true }
        Logger(fmt.Sprintf("%-40s %s=%t", "OTE_MASTERSPY="+envvar, "masterSpy", addMasterSpy))

        _, _ = ote( txs, chans, orderers, ordType, kbs, optimizeClients, addMasterSpy )

        CloseLogger()
}
