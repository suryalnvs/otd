
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

package main        // Orderer Test Engine

import (
        //"fmt"
        "testing"
)


// input args:  ote ( txs int64, chans int, orderers int, ordererType string, kbs int )
// outputs:     print report to stdout with lots of counters!
// returns:     passResult, finalResultSummaryString


/*    THIS SOLO test is ready, once we create a docker-compose file for it, or integrate dongming's tool...
func Test_100TX_1ch_1ord_Solo(t *testing.T) {
        fmt.Println("Send 100 TX on 1 channel to 1 orderer of type Solo")
        passResult, finalResultSummaryString := ote(100, 1, 1, "solo", 0 )
        t.Log(finalResultSummaryString)
        if !passResult { t.Fail() }
}
*/


/*
func Test_1000TX_1ch_1ord_kafka_3kbrokers(t *testing.T) {
        fmt.Println("\nStart ote_test: Send 1000 TX on 1 channel to 1 orderer of type kafka using 3 kafka-brokers")
        passResult, finalResultSummaryString := ote(1000, 1, 1, "kafka", 3 )
        if !passResult { t.Error(finalResultSummaryString) }
}

func Test_1000TX_9ch_3ord_kafka_3kbs(t *testing.T) {
        fmt.Println("\nStart ote_test: Send 1000 TX on 9 channel to 3 orderers of type kafka using 3 kafka-brokers")
        passResult, finalResultSummaryString := ote(1000, 9, 3, "kafka", 3 )
        if !passResult { t.Error(finalResultSummaryString) }
}
*/

func Test_100TX_1ch_3ord_kafka_3kbs(t *testing.T) {
        passResult, finalResultSummaryString := ote(100, 1, 3, "kafka", 3 )
        if !passResult { t.Error(finalResultSummaryString) }
}

func Test_100TX_2ch_1ord_kafka_3kbs(t *testing.T) {
        passResult, finalResultSummaryString := ote(100, 2, 1, "kafka", 3 )
        if !passResult { t.Error(finalResultSummaryString) }
}

func Test_100TX_2ch_3ord_kafka_3kbs(t *testing.T) {
        passResult, finalResultSummaryString := ote(100, 2, 3, "kafka", 3 )
        if !passResult { t.Error(finalResultSummaryString) }
}

