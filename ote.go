
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

// Orderer Traffic Engine
// ======================
//
// This file ote.go contains main(), for executing from command line
// using environment variables to override those in orderer/orderer.yaml
// or to set OTE test configuration parameters.
//
// Function ote() is called by main after reading environment variables,
// and is also called via "go test" from tests in ote_test.go. Those
// tests can be executed from automated Continuous Integration processes,
// which can use https://github.com/jstemmer/go-junit-report to convert the
// logs to produce junit output for CI reports.
//   go get github.com/jstemmer/go-junit-report
//   go test -v | go-junit-report > report.xml 
//
// ote() invokes tool driver.sh (including network.json and json2yml.js) -
//   which is only slightly modified from the original version at
//   https://github.com/dongmingh/v1FabricGenOption -
//   to launch an orderer service network per the specified parameters
//   (including kafka brokers or other necessary support processes).
//   Function ote() performs several actions:
// + create Producer clients to connect via grpc to all the channels on
//   all the orderers to send/broadcast transaction messages 
// + create Consumer clients to connect via grpc to ListenAddress:ListenPort
//   on all channels on all orderers and call deliver() to receive messages
//   containing batches of transactions
// + use parameters for specifying test configuration such as:
//   number of transactions, number of channels, number of orderers ...
// + load orderer/orderer.yml to retrieve environment variables used for
//   overriding orderer configuration such as batchsize, batchtimeout ...
// + generate unique transactions, dividing up the requested OTE_TXS count
//   among all the Producers
// + Consumers confirm the same number of blocks and TXs are delivered
//   by all the orderers on all the channels
// + print logs for any errors, and print final tallied results
// + return a pass/fail result and a result summary string

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

var debugflagAPI bool = true
var debugflag1 bool = false
var debugflag2 bool = false
var debugflag3 bool = false // most detailed and voluminous

var producers_wg sync.WaitGroup
var logFile *os.File
var logEnabled bool = false
var envvar string = ""

var numChannels int = 1
var numOrdsInNtwk  int = 1
var numOrdsToWatch int = 1
var ordererType string = "solo"
var numKBrokers int = 0
var producersPerCh int = 1
var numConsumers int = 1
var numProducers int = 1

// numTxToSend is the total number of Transactions to send; A fraction is
// sent by each producer for each channel for each orderer.

var numTxToSend int64 = 1

// One GO thread is created for each producer and each consumer client.
// To optimize go threads usage, to prevent running out of swap space
// in the (laptop) test environment for tests using either numerous
// channels or numerous producers per channel, set bool optimizeClientsMode
// true to only create one go thread MasterProducer per orderer, which will
// broadcast messages to all channels on one orderer. Note this option
// works a little less efficiently on the consumer side, where we
// share a single grpc connection but still need to use separate
// GO threads per channel per orderer (instead of one per orderer).

var optimizeClientsMode bool = false

// ordStartPort (default port is 7050, but driver.sh uses 5005).

var ordStartPort uint16 = 5005

func resetGlobals() {
        // When running multiple tests, e.g. from go test, reset to defaults
        // for the parameters that could change per test.
        // We do NOT reset things that would apply to every test, such as
        // settings for environment variables
        logEnabled = false
        envvar = ""
        numChannels = 1
        numOrdsInNtwk = 1
        numOrdsToWatch = 1
        ordererType = "solo"
        numKBrokers = 0
        numConsumers = 1
        numProducers = 1
        numTxToSend = 1
        producersPerCh = 1
}

func InitLogger(fileName string) {
        if !logEnabled {
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
}

func Logger(printStmt string) {
        fmt.Println(printStmt)
        if !logEnabled {
                return
        }
        log.Println(printStmt)
}

func CloseLogger() {
        if logFile != nil {
                logFile.Close()
        }
        logEnabled = false
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
                                if debugflag2 { Logger(fmt.Sprintf("Consumer recvd a block, o %d c %d blkNum %d numtrans %d", ordererIndex, channelIndex, t.Block.Header.Number, len(t.Block.Data.Data))) }
                                if debugflag3 { Logger(fmt.Sprintf("blk: %v", t.Block.Data.Data)) }
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

func launchNetwork(appendFlags string) {
        // Alternative way: hardcoded docker compose (not driver.sh tool)
        //  _ = executeCmd("docker-compose -f docker-compose-3orderers.yml up -d")

        cmd := fmt.Sprintf("./driver_GenOpt.sh -a create -p 1 %s", appendFlags)
        Logger(fmt.Sprintf("Launching network:  %s", cmd))
        if debugflagAPI {
                executeCmdAndDisplay(cmd) // show stdout logs; debugging help
        } else {
                executeCmd(cmd)
        }

        // display the network of docker containers with the orderers and such
        executeCmdAndDisplay("docker ps -a")
}

func countGenesis() int64 {
        return int64(numChannels)
}
func sendEqualRecv(numTxToSend int64, totalTxRecv_p *[]int64, totalTxRecvMismatch bool, totalBlockRecvMismatch bool) bool {
        var matching = false;
        if (*totalTxRecv_p)[0] == numTxToSend {
                // recv count on orderer 0 matches the send count
                if !totalTxRecvMismatch && !totalBlockRecvMismatch {
                        // all orderers have same recv counters
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
                        (*txSentCntr_p)++
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
                // send one TX to every broadcast client (one TX on each chnl)
                for c := 0; c < numChannels; c++ {
                        if i < (*txReq_p)[c] {
                                // more TXs to send on this channel
                                bc[c].broadcast([]byte(fmt.Sprintf("Testing %v", time.Now())))
                                err = bc[c].getAck()
                                if err == nil {
                                        (*txSentCntr_p)[c]++
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
        // The counters for Producers are indexed by orderer (numOrdsInNtwk)
        // and channel (numChannels).
        // Total count includes all counters for all channels on ALL orderers.
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

        // Counters for consumers are indexed by orderer (numOrdsToWatch)
        // and channel (numChannels).
        // The total count includes all counters for all channels on
        // ONLY ONE orderer.
        // Tally up the totals for all the channels on each orderer, and
        // store them for comparison; they should all be the same.
        // e.g.    totalTxRecv[k]    = sum of txRecv[k][*]
        // e.g.    totalBlockRecv[k] = sum of blockRecv[k][*]

        *totalTxRecvMismatch = false
        *totalBlockRecvMismatch = false
        for k := 0; k < numOrdsToWatch; k++ {
                // count only the requested TXs - not the genesis block TXs
                (*totalTxRecv)[k] = -countGenesis()
                (*totalBlockRecv)[k] = -countGenesis()
                for l := 0; l < numChannels; l++ {
                        (*totalTxRecv)[k] += (*txRecv)[k][l]
                        (*totalBlockRecv)[k] += (*blockRecv)[k][l]
                        if debugflag3 { Logger(fmt.Sprintf("in compute(): k %d l %d txRecv[k][l] %d blockRecv[k][l] %d", k , l , (*txRecv)[k][l] , (*blockRecv)[k][l] )) }
                }
                if (k>0) && (*totalTxRecv)[k] != (*totalTxRecv)[k-1] { *totalTxRecvMismatch = true }
                if (k>0) && (*totalBlockRecv)[k] != (*totalBlockRecv)[k-1] { *totalBlockRecvMismatch = true }
        }
        if debugflag2 { Logger(fmt.Sprintf("in compute(): totalTxRecv[]= %v, totalBlockRecv[]= %v", *totalTxRecv, *totalBlockRecv)) }
}

func reportTotals(testname string, numTxToSendTotal int64, countToSend [][]int64, txSent [][]int64, totalNumTxSent int64, txSentFailures [][]int64, totalNumTxSentFailures int64, batchSize int64, txRecv [][]int64, totalTxRecv []int64, totalTxRecvMismatch bool, blockRecv [][]int64, totalBlockRecv []int64, totalBlockRecvMismatch bool, masterSpy bool, channelIDs *[]string) (successResult bool, resultStr string) {

        // default to failed
        var passFailStr string = "FAILED"
        successResult = false
        resultStr = "TEST " + testname + " "

        // For each Producer, print the ordererIndex and channelIndex, the
        // number of TX requested to be sent, the actual number of TX sent,
        // and the number we failed to send.

        if numOrdsInNtwk > 3 || numChannels > 3 {
                Logger(fmt.Sprintf("Print only the first 3 chans of only the first 3 ordererIdx; and any others ONLY IF they contain failures.\nTotals numOrdInNtwk=%d numChan=%d numPRODUCERs=%d", numOrdsInNtwk, numChannels, numOrdsInNtwk*numChannels))
        }
        Logger("PRODUCERS   OrdererIdx  ChannelIdx ChannelID              TX Target         ACK        NACK")
        for i := 0; i < numOrdsInNtwk; i++ {
                for j := 0; j < numChannels; j++ {
                        if (i < 3 && j < 3) || txSentFailures[i][j] > 0 || countToSend[i][j] != txSent[i][j] + txSentFailures[i][j] {
                                Logger(fmt.Sprintf("%22d%12d %-20s%12d%12d%12d",i,j,(*channelIDs)[j],countToSend[i][j],txSent[i][j],txSentFailures[i][j]))
                        } else if (i < 3 && j == 3) {
                                Logger(fmt.Sprintf("%34s","..."))
                        } else if (i == 3 && j == 0) {
                                Logger(fmt.Sprintf("%22s","..."))
                        }
                }
        }

        // for each consumer print the ordererIndex & channel, the num blocks and the num transactions received/delivered
        if numOrdsToWatch > 3 || numChannels > 3 {
                Logger(fmt.Sprintf("Print only the first 3 chans of only the first 3 ordererIdx (and the last ordererIdx if masterSpy is present), plus any others that contain failures.\nTotals numOrdIdx=%d numChanIdx=%d numCONSUMERS=%d", numOrdsToWatch, numChannels, numOrdsToWatch*numChannels))
        }
        Logger("CONSUMERS   OrdererIdx  ChannelIdx ChannelID                Batches         TXs")
        for i := 0; i < numOrdsToWatch; i++ {
                for j := 0; j < numChannels; j++ {
                        if (j < 3 && (i < 3 || (masterSpy && i==numOrdsInNtwk-1))) || (i>1 && (blockRecv[i][j] != blockRecv[1][j] || txRecv[1][j] != txRecv[1][j])) {
                                // Subtract one from the received Block count and TX count, to ignore the genesis block
                                // (we already ignore genesis blocks when we compute the totals in totalTxRecv[n] , totalBlockRecv[n])
                                Logger(fmt.Sprintf("%22d%12d %-20s%12d%12d",i,j,(*channelIDs)[j],blockRecv[i][j]-1,txRecv[i][j]-1))
                        } else if (i < 3 && j == 3) {
                                Logger(fmt.Sprintf("%34s","..."))
                        } else if (i == 3 && j == 0) {
                                Logger(fmt.Sprintf("%22s","..."))
                        }
                }
        }

        // Check for differences on the deliveries from the orderers. These are
        // probably errors - unless the test stopped an orderer on purpose and
        // never restarted it, while the others continued to deliver TXs.
        // (If an orderer is restarted, then it would reprocess all the
        // back-ordered transactions to catch up with the others.)

        if totalTxRecvMismatch { Logger("!!!!! Num TXs Delivered is not same on all orderers!!!!!") }
        if totalBlockRecvMismatch { Logger("!!!!! Num Blocks Delivered is not same on all orderers!!!!!") }

        if totalTxRecv[0] == numTxToSendTotal {
                // recv count on orderer 0 matches the send count
                if !totalTxRecvMismatch && !totalBlockRecvMismatch {
                        Logger("Hooray! Every TX was successfully sent AND delivered by orderer service.")
                        successResult = true
                        passFailStr = "PASSED"
                } else {
                        resultStr += "Orderers were INCONSISTENT: Every TX was successfully sent AND delivered by orderer0 but not all orderers"
                }
        } else if totalTxRecv[0] == totalNumTxSent + totalNumTxSentFailures {
                if !totalTxRecvMismatch && !totalBlockRecvMismatch {
                        resultStr += "Every ACked TX was delivered, but failures occurred:"
                } else {
                        resultStr += "Orderers were INCONSISTENT: Every ACked TX was delivered, but failures occurred:"
                }
        } else {
                resultStr += "BAD! Some ACKed TX were LOST by orderer service!"
        }

        ////////////////////////////////////////////////////////////////////////
        //
        // Before we declare success, let's check some more things...
        //
        // At this point, we have decided if most of the numbers make sense by
        // setting succssResult to true if the tests passed. Thus we assume
        // successReult=true and just set it to false if we find a problem.

        // Check the totals to verify if the number of blocks on each channel
        // is appropriate for the given batchSize and number of TXs sent.

        expectedBlocksOnChan := make([]int64, numChannels) // create a counter for all the channels on one orderer
        for c := 0; c < numChannels; c++ {
                var chanSentTotal int64 = 0
                for ord := 0; ord < numOrdsInNtwk; ord++ {
                        chanSentTotal += txSent[ord][c]
                }
                expectedBlocksOnChan[c] = chanSentTotal / batchSize
                if chanSentTotal % batchSize > 0 { expectedBlocksOnChan[c]++ }
                for ord := 0; ord < numOrdsToWatch; ord++ {
                        if expectedBlocksOnChan[c] != blockRecv[ord][c] - 1 { // ignore genesis block
                                successResult = false
                                passFailStr = "FAILED"
                                Logger(fmt.Sprintf("Error: Unexpected Block count %d (expected %d) on ordIndx=%d channelIDs[%d]=%s, chanSentTxTotal=%d BatchSize=%d", blockRecv[ord][c]-1, expectedBlocksOnChan[c], ord, c, (*channelIDs)[c], chanSentTotal, batchSize))
                        } else {
                                if debugflag1 { Logger(fmt.Sprintf("GOOD block count %d on ordIndx=%d channelIDs[%d]=%s chanSentTxTotal=%d BatchSize=%d", expectedBlocksOnChan[c], ord, c, (*channelIDs)[c], chanSentTotal, batchSize)) }
                        }
                }
        }


        // TODO - Verify the contents of the last block of transactions.
        //        Since we do not know exactly what should be in the block,
        //        then at least we can do:
        //            for each channel, verify if the block delivered from
        //            each orderer is the same (i.e. contains the same
        //            Data bytes (transactions) in the last block)


        // print some counters totals
        Logger(fmt.Sprintf("Not counting genesis blks (1 per chan)%9d", countGenesis()))
        Logger(fmt.Sprintf("Total TX broadcasts Requested to Send %9d", numTxToSendTotal))
        Logger(fmt.Sprintf("Total TX broadcasts send success ACK  %9d", totalNumTxSent))
        Logger(fmt.Sprintf("Total TX broadcasts sendFailed - NACK %9d", totalNumTxSentFailures))
        Logger(fmt.Sprintf("Total LOST transactions               %9d", totalNumTxSent + totalNumTxSentFailures - totalTxRecv[0] ))
        if successResult {
                Logger(fmt.Sprintf("Total deliveries received TX          %9d", totalTxRecv[0]))
                Logger(fmt.Sprintf("Total deliveries received Blocks      %9d", totalBlockRecv[0]))
        } else {
                Logger(fmt.Sprintf("Total deliveries received TX on each ordrr     %7d", totalTxRecv))
                Logger(fmt.Sprintf("Total deliveries received Blocks on each ordrr %7d", totalBlockRecv))
        }

        // print output result and counts : overall summary
        resultStr += fmt.Sprintf(" RESULT=%s: TX Req=%d BrdcstACK=%d NACK=%d DelivBlk=%d DelivTX=%d numChannels=%d batchSize=%d", passFailStr, numTxToSendTotal, totalNumTxSent, totalNumTxSentFailures, totalBlockRecv, totalTxRecv, numChannels, batchSize)
        Logger(fmt.Sprintf(resultStr))

        return successResult, resultStr
}

// Function:    ote - the Orderer Test Engine
// Outputs:     print report to stdout with lots of counters
// Returns:     passed bool, resultSummary string
func ote( testname string, txs int64, chans int, orderers int, ordType string, kbs int, masterSpy bool, pPerCh int ) (passed bool, resultSummary string) {

        passed = false
        resultSummary = testname + " test not completed: INPUT ERROR: "
        resetGlobals()
        InitLogger("ote")
        defer CloseLogger()

        Logger(fmt.Sprintf("========== OTE testname=%s TX=%d Channels=%d Orderers=%d ordererType=%s kafka-brokers=%d addMasterSpy=%t producersPerCh=%d", testname, txs, chans, orderers, ordType, kbs, masterSpy, pPerCh))

        // Establish the default configuration from yaml files - and this also
        // picks up any variables overridden on command line or in environment
        config := config.Load()  // establish the default configuration from yaml files,
        var launchAppendFlags string

        ////////////////////////////////////////////////////////////////////////
        // Check parameters and/or env vars to see if user wishes to override
        // default config parms.
        ////////////////////////////////////////////////////////////////////////

        //////////////////////////////////////////////////////////////////////
        // Arguments for OTE settings for test variations:
        //////////////////////////////////////////////////////////////////////

        if txs > 0        { numTxToSend = txs   }      else { return passed, resultSummary + "number of transactions must be > 0" }
        if chans > 0      { numChannels = chans }      else { return passed, resultSummary + "number of channels must be > 0" }
        if orderers > 0   {
                numOrdsInNtwk = orderers
                launchAppendFlags += fmt.Sprintf(" -o %d", orderers)
        } else { return passed, resultSummary + "number of orderers in network must be > 0" }

        if pPerCh > 1 {
                producersPerCh = pPerCh
                return passed, resultSummary + "Multiple producersPerChannel NOT SUPPORTED yet."
        }

        numOrdsToWatch = numOrdsInNtwk    // Watch every orderer to verify they are all delivering the same.
        if masterSpy { numOrdsToWatch++ } // We are not creating another orderer here, but we do need
                                          // another set of counters; the masterSpy will be created for
                                          // this test to watch every channel on an orderer - so that means
                                          // one orderer is being watched twice

        // this is not an argument, but user may set this tuning parameter before running test
        envvar = os.Getenv("OTE_CLIENTS_SHARE_CONNS")
        if envvar != "" {
                if (strings.ToLower(envvar) == "true" || strings.ToLower(envvar) == "t") {
                        optimizeClientsMode = true
                }
                if debugflagAPI {
                        Logger(fmt.Sprintf("%-50s %s=%t", "OTE_CLIENTS_SHARE_CONNS="+envvar, "optimizeClientsMode", optimizeClientsMode))
                        Logger("Setting OTE_CLIENTS_SHARE_CONNS option to true does the following:\n1. All Consumers on an orderer (one GO thread per each channel) will share grpc connection.\n2. All Producers on an orderer will share a grpc conn AND share one GO-thread.\nAlthough this reduces concurrency and lengthens the test duration, it satisfies\nthe objective of reducing swap space requirements and should be selected when\nrunning tests with numerous channels or producers per channel.")
                }
        }
        if optimizeClientsMode {
                // use only one MasterProducer and one MasterConsumer on each orderer
                numProducers = numOrdsInNtwk
                numConsumers = numOrdsInNtwk
        } else {
                // one Producer and one Consumer for EVERY channel on each orderer
                numProducers = numOrdsInNtwk * numChannels
                numConsumers = numOrdsInNtwk * numChannels
        }

        //////////////////////////////////////////////////////////////////////
        // Arguments to override configuration parameter values in yaml file:
        //////////////////////////////////////////////////////////////////////

        // ordererType
        ordererType = config.Genesis.OrdererType
        if ordType != "" {
                ordererType = ordType
        } else {
                Logger(fmt.Sprintf("Null value provided for ordererType; using value from config file: %s", ordererType))
        }
        launchAppendFlags += fmt.Sprintf(" -t %s", ordererType)
        if "kafka" == strings.ToLower(ordererType) {
                if kbs > 0 {
                        numKBrokers = kbs
                        launchAppendFlags += fmt.Sprintf(" -k %d", numKBrokers)
                } else {
                        return passed, resultSummary + "When using kafka ordererType, number of kafka-brokers must be > 0"
                }
        } else { numKBrokers = 0 }

        // batchSize is not an argument of ote(), but this orderer.yaml config
        // variable may be overridden on command line or by exporting it.
        batchSize := int64(config.Genesis.BatchSize.MaxMessageCount) // retype the uint32
        envvar = os.Getenv("ORDERER_GENESIS_BATCHSIZE_MAXMESSAGECOUNT")
        if envvar != "" { launchAppendFlags += fmt.Sprintf(" -b %d", batchSize) }
        if debugflagAPI { Logger(fmt.Sprintf("%-50s %s=%d", "ORDERER_GENESIS_BATCHSIZE_MAXMESSAGECOUNT="+envvar, "batchSize", batchSize)) }

        // batchTimeout
        //Logger(fmt.Sprintf("DEBUG=====BatchTimeout config:%v config.Seconds-float():%v config.Seconds-int:%v", config.Genesis.BatchTimeout, (config.Genesis.BatchTimeout).Seconds(), int((config.Genesis.BatchTimeout).Seconds())))
        batchTimeout := int((config.Genesis.BatchTimeout).Seconds()) // Seconds() converts time.Duration to float64, and then retypecast to int
        envvar = os.Getenv("ORDERER_GENESIS_BATCHTIMEOUT")
        if envvar != "" { launchAppendFlags += fmt.Sprintf(" -c %d", batchTimeout) }
        if debugflagAPI { Logger(fmt.Sprintf("%-50s %s=%d", "ORDERER_GENESIS_BATCHTIMEOUT="+envvar, "batchTimeout", batchTimeout)) }

        // CoreLoggingLevel
        envvar = strings.ToUpper(os.Getenv("CORE_LOGGING_LEVEL")) // (default = not set)|CRITICAL|ERROR|WARNING|NOTICE|INFO|DEBUG
        if envvar != "" {
                launchAppendFlags += fmt.Sprintf(" -l %s", envvar)
        }
        if debugflagAPI { Logger(fmt.Sprintf("CORE_LOGGING_LEVEL=%s", envvar)) }

        // CoreLedgerStateDB
        envvar = os.Getenv("CORE_LEDGER_STATE_STATEDATABASE")  // goleveldb | CouchDB
        if envvar != "" {
                launchAppendFlags += fmt.Sprintf(" -d %s", envvar)
        }
        if debugflagAPI { Logger(fmt.Sprintf("CORE_LEDGER_STATE_STATEDATABASE=%s", envvar)) }

        // CoreSecurityLevel
        envvar = os.Getenv("CORE_SECURITY_LEVEL")  // 256 | 384
        if envvar != "" {
                launchAppendFlags += fmt.Sprintf(" -w %s", envvar)
        }
        if debugflagAPI { Logger(fmt.Sprintf("CORE_SECURITY_LEVEL=%s", envvar)) }

        // CoreSecurityHashAlgorithm
        envvar = os.Getenv("CORE_SECURITY_HASHALGORITHM")  // SHA2 | SHA3
        if envvar != "" {
                launchAppendFlags += fmt.Sprintf(" -x %s", envvar)
        }
        if debugflagAPI { Logger(fmt.Sprintf("CORE_SECURITY_HASHALGORITHM=%s", envvar)) }


        //////////////////////////////////////////////////////////////////////////
        // Each producer sends TXs to one channel on one orderer, and increments
        // its own counters for the successfully sent Tx, and the send-failures
        // (rejected/timeout). These arrays are indexed by dimensions:
        // numOrdsInNtwk and numChannels

        var countToSend        [][]int64
        var txSent             [][]int64
        var txSentFailures     [][]int64
        var totalNumTxSent         int64 = 0
        var totalNumTxSentFailures int64 = 0

        // Each consumer receives blocks delivered on one channel from one
        // orderer, and must track its own counters for the received number of
        // blocks and received number of Tx.
        // We will create consumers for every channel on an orderer, and total
        // up the TXs received. And do that for all the orderers (indexed by
        // numOrdsToWatch). We will check to ensure all the orderers receive
        // all the same deliveries. These arrays are indexed by dimensions:
        // numOrdsToWatch and numChannels

        var txRecv       [][]int64
        var blockRecv    [][]int64
        var totalTxRecv    []int64 // total TXs rcvd by all consumers on an orderer, indexed by numOrdsToWatch
        var totalBlockRecv []int64 // total Blks recvd by all consumers on an orderer, indexed by numOrdsToWatch
        var totalTxRecvMismatch bool = false
        var totalBlockRecvMismatch bool = false
        var consumerConns [][]*grpc.ClientConn

        ////////////////////////////////////////////////////////////////////////
        // Create the 1D and 2D slices of counters for the producers and
        // consumers. All are initialized to zero.

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


        ////////////////////////////////////////////////////////////////////////

        launchNetwork(launchAppendFlags)
        time.Sleep(10 * time.Second)

        ////////////////////////////////////////////////////////////////////////
        // Create the 1D slice of channel IDs, and create names for them
        // which we will use when producing/broadcasting/sending msgs and
        // consuming/delivering/receiving msgs.

        var channelIDs []string
        channelIDs = make([]string, numChannels)

        // TODO (after FAB-2001 and FAB-2083 are fixed) - Remove the if-then clause.
        // Due to those bugs, we cannot pass many tests using multiple orderers and multiple channels.
        // TEMPORARY PARTIAL SOLUTION: To test multiple orderers with a single channel,
        // use hardcoded TestChainID and skip creating any channels.
    if numChannels == 1 {
      channelIDs[0] = provisional.TestChainID
      Logger(fmt.Sprintf("Using DEFAULT channelID = %s", channelIDs[0]))
    } else {
        Logger(fmt.Sprintf("Using %d new channelIDs, e.g. test-chan.00023", numChannels))
        for c:=0; c < numChannels; c++ {
                channelIDs[c] = fmt.Sprintf("test-chan.%05d", c)
                cmd := fmt.Sprintf("cd $GOPATH/src/github.com/hyperledger/fabric && CORE_PEER_COMMITTER_LEDGER_ORDERER=127.0.0.1:%d peer channel create -c %s", ordStartPort, channelIDs[c])
                _ = executeCmd(cmd)
                //executeCmdAndDisplay(cmd)
        }
    }

        ////////////////////////////////////////////////////////////////////////
        // Start threads for each consumer to watch each channel on all (the
        // specified number of) orderers. This code assumes orderers in the
        // network will use increasing port numbers, which is the same logic
        // used by the driver.sh tool that starts the network for us: the first
        // orderer uses ordStartPort, the second uses ordStartPort+1, etc.

        for ord := 0; ord < numOrdsToWatch; ord++ {
                serverAddr := fmt.Sprintf("%s:%d", config.General.ListenAddress, ordStartPort + uint16(ord))
                if masterSpy && ord == numOrdsToWatch-1 {
                        // Special case: this is the last row of counters,
                        // added (and incremented numOrdsToWatch) for the
                        // masterSpy to use to watch the first orderer for
                        // deliveries, on all channels. This will be a duplicate
                        // Consumer (it is the second one monitoring the first
                        // orderer), so we need to reuse the first port.
                        serverAddr = fmt.Sprintf("%s:%d", config.General.ListenAddress, ordStartPort)
                        go startConsumerMaster(serverAddr, &channelIDs, ord, &(txRecv[ord]), &(blockRecv[ord]), &(consumerConns[ord][0]))
                } else
                if optimizeClientsMode {
                        // Create just one Consumer to receive all deliveries
                        // (on all channels) on an orderer.
                        go startConsumerMaster(serverAddr, &channelIDs, ord, &(txRecv[ord]), &(blockRecv[ord]), &(consumerConns[ord][0]))
                } else {
                        // Normal mode: create a unique consumer client
                        // go-thread for each channel on each orderer.
                        for c := 0 ; c < numChannels ; c++ {
                                go startConsumer(serverAddr, channelIDs[c], ord, c, &(txRecv[ord][c]), &(blockRecv[ord][c]), &(consumerConns[ord][c]))
                        }
                }

        }

        Logger("Finished creating all CONSUMERS clients")
        time.Sleep(5 * time.Second)
        defer cleanNetwork(&consumerConns)

        ////////////////////////////////////////////////////////////////////////
        // Now that the orderer service network is running, and the consumers
        // are watching for deliveries, we can start clients which will
        // broadcast the specified number of TXs to their associated orderers.

        if optimizeClientsMode {
                producers_wg.Add(numOrdsInNtwk)
        } else {
                producers_wg.Add(numProducers)
        }
        sendStart := time.Now().Unix()
        for ord := 0; ord < numOrdsInNtwk; ord++ {
                serverAddr := fmt.Sprintf("%s:%d", config.General.ListenAddress, ordStartPort + uint16(ord))
                for c := 0 ; c < numChannels ; c++ {
                        countToSend[ord][c] = numTxToSend / int64(numOrdsInNtwk * numChannels)
                        if c==0 && ord==0 { countToSend[ord][c] += numTxToSend % int64(numOrdsInNtwk * numChannels) }
                }
                if optimizeClientsMode {
                        // create one Producer for all channels on this orderer
                        go startProducerMaster(serverAddr, &channelIDs, ord, &(countToSend[ord]), &(txSent[ord]), &(txSentFailures[ord]))
                } else {
                        // Normal mode: create a unique consumer client
                        // go thread for each channel
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

        ////////////////////////////////////////////////////////////////////////
        // All producer threads are finished sending broadcast transactions.
        // Let's determine if the deliveries have all been received by the
        // consumer threads. We will check if the receive counts match the send
        // counts on all consumers, or if all consumers are no longer receiving
        // blocks. Wait and continue rechecking as necessary, as long as the
        // delivery (recv) counters are climbing closer to the broadcast (send)
        // counter. If the counts do not match, wait for up to batchTimeout
        // seconds, to ensure that we received the last (non-full) batch.

        computeTotals(&txSent, &totalNumTxSent, &txSentFailures, &totalNumTxSentFailures, &txRecv, &totalTxRecv, &totalTxRecvMismatch, &blockRecv, &totalBlockRecv, &totalBlockRecvMismatch)

        waitSecs := 0
        for !sendEqualRecv(numTxToSend, &totalTxRecv, totalTxRecvMismatch, totalBlockRecvMismatch) && (moreDeliveries(&txSent, &totalNumTxSent, &txSentFailures, &totalNumTxSentFailures, &txRecv, &totalTxRecv, &totalTxRecvMismatch, &blockRecv, &totalBlockRecv, &totalBlockRecvMismatch) || waitSecs < batchTimeout) { time.Sleep(1 * time.Second); waitSecs++ }

        // Recovery Duration = time spent waiting for orderer service to finish delivering transactions,
        // after all producers finished sending them.
        // waitSecs = some possibly idle time spent waiting for the last batch to be generated (waiting for batchTimeout)
        Logger(fmt.Sprintf("Recovery Duration (secs):%4d", time.Now().Unix() - recoverStart))
        Logger(fmt.Sprintf("waitSecs for last batch: %4d", waitSecs))
        passed, resultSummary = reportTotals(testname, numTxToSend, countToSend, txSent, totalNumTxSent, txSentFailures, totalNumTxSentFailures, batchSize, txRecv, totalTxRecv, totalTxRecvMismatch, blockRecv, totalBlockRecv, totalBlockRecvMismatch, masterSpy, &channelIDs)

        return passed, resultSummary
}

func main() {

        resetGlobals()
        InitLogger("ote")

        // Set reasonable defaults in case any env vars are unset.
        var txs int64 = 55
        chans    := numChannels
        orderers := numOrdsInNtwk
        ordType  := ordererType
        kbs      := numKBrokers


        // Set addMasterSpy to true to create one additional consumer client
        // that monitors all channels on one orderer with one grpc connection.
        addMasterSpy := false

        pPerCh := producersPerCh
        // TODO lPerCh := listenersPerCh

        // Read env vars
        if debugflagAPI { Logger("==========Environment variables provided for this test, and corresponding values actually used for the test:") }
        testcmd := ""
        envvar := os.Getenv("OTE_TXS")
        if envvar != "" { txs, _ = strconv.ParseInt(envvar, 10, 64); testcmd += " OTE_TXS="+envvar }
        if debugflagAPI { Logger(fmt.Sprintf("%-50s %s=%d", "OTE_TXS="+envvar, "txs", txs)) }

        envvar = os.Getenv("OTE_CHANNELS")
        if envvar != "" { chans, _ = strconv.Atoi(envvar); testcmd += " OTE_CHANNELS="+envvar }
        if debugflagAPI { Logger(fmt.Sprintf("%-50s %s=%d", "OTE_CHANNELS="+envvar, "chans", chans)) }

        envvar = os.Getenv("OTE_ORDERERS")
        if envvar != "" { orderers, _ = strconv.Atoi(envvar); testcmd += " OTE_ORDERERS="+envvar }
        if debugflagAPI { Logger(fmt.Sprintf("%-50s %s=%d", "OTE_ORDERERS="+envvar, "orderers", orderers)) }

        envvar = os.Getenv("ORDERER_GENESIS_ORDERERTYPE")
        if envvar != "" { ordType = envvar; testcmd += " ORDERER_GENESIS_ORDERERTYPE="+envvar }
        if debugflagAPI { Logger(fmt.Sprintf("%-50s %s=%s", "ORDERER_GENESIS_ORDERERTYPE="+envvar, "ordType", ordType)) }

        envvar = os.Getenv("OTE_KAFKABROKERS")
        if envvar != "" { kbs, _ = strconv.Atoi(envvar); testcmd += " OTE_KAFKABROKERS="+envvar }
        if debugflagAPI { Logger(fmt.Sprintf("%-50s %s=%d", "OTE_KAFKABROKERS="+envvar, "kbs", kbs)) }

        envvar = os.Getenv("OTE_MASTERSPY")
        if "true" == strings.ToLower(envvar) || "t" == strings.ToLower(envvar) { addMasterSpy = true; testcmd += " OTE_MASTERSPY="+envvar }
        if debugflagAPI { Logger(fmt.Sprintf("%-50s %s=%t", "OTE_MASTERSPY="+envvar, "masterSpy", addMasterSpy)) }

        envvar = os.Getenv("OTE_PRODUCERS_PER_CHANNEL")
        if envvar != "" { pPerCh, _ = strconv.Atoi(envvar); testcmd += " OTE_PRODUCERS_PER_CHANNEL="+envvar }
        if debugflagAPI { Logger(fmt.Sprintf("%-50s %s=%d", "OTE_PRODUCERS_PER_CHANNEL="+envvar, "producersPerCh", pPerCh)) }

        _, _ = ote( "<commandline>"+testcmd+" ote", txs, chans, orderers, ordType, kbs, addMasterSpy, pPerCh)
}
