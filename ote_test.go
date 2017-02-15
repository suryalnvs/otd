
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
        "fmt"
        "testing"
)

// simplest testcase
func Test_1tx_1ch_1ord_Solo(t *testing.T) {
        fmt.Println("\nSimplest test: Send 1 TX on 1 channel to 1 Solo orderer")
        passResult, finalResultSummaryString := ote("Test_1tx_1ch_1ord_Solo", 1, 1, 1, "solo", 0, false, 1 )
        t.Log(finalResultSummaryString)
        if !passResult { t.Fail() }
}

// 77
// 78 = 77 rerun with ORDERER_GENESIS_BATCHTIMEOUT_MAXMESSAGECOUNT=500
func Test_ORD77_ORD78_10000TX_1ch_1ord_solo_batchIT(t *testing.T) {
        //fmt.Println("Send 10,000 TX on 1 channel to 1 Solo orderer")
        passResult, finalResultSummaryString := ote("ORD-77_ORD-78", 10000, 1, 1, "solo", 0, false, 1 )
        if !passResult { t.Error(finalResultSummaryString) }
}

// 79
// 80 = rerun with ORDERER_GENESIS_BATCHTIMEOUT_MAXMESSAGECOUNT=500
func Test_ORD79_ORD80_10000TX_1ch_1ord_kafka_1kbs_batchIT(t *testing.T) {
        passResult, finalResultSummaryString := ote("ORD-79,ORD-80", 10000, 1, 1, "kafka", 1, false, 1 )
        if !passResult { t.Error(finalResultSummaryString) }
}

// 81
// 82 = rerun with ORDERER_GENESIS_BATCHTIMEOUT_MAXMESSAGECOUNT=500
func Test_ORD81_ORD82_10000TX_3ch_1ord_kafka_3kbs_batchIT(t *testing.T) {
        passResult, finalResultSummaryString := ote("ORD-81,ORD-82", 10000, 3, 1, "kafka", 3, false, 1 )
        if !passResult { t.Error(finalResultSummaryString) }
}

// 83
// 84 = rerun with ORDERER_GENESIS_BATCHTIMEOUT_MAXMESSAGECOUNT=500
func Test_ORD83_ORD84_10000TX_3ch_3ord_kafka_3kbs_batchIT(t *testing.T) {
        passResult, finalResultSummaryString := ote("ORD-83,ORD-84", 10000, 3, 3, "kafka", 3, false, 1 )
        if !passResult { t.Error(finalResultSummaryString) }
}

// 85
func Test_ORD85_1000000TX_1ch_3ord_kafka_3kbs_spy(t *testing.T) {
        passResult, finalResultSummaryString := ote("ORD-85", 1000000, 1, 3, "kafka", 3, true, 1 )
        if !passResult { t.Error(finalResultSummaryString) }
}

// 86
func Test_ORD86_1000000TX_3ch_1ord_kafka_3kbs_spy(t *testing.T) {
        passResult, finalResultSummaryString := ote("ORD-86", 1000000, 1, 1, "kafka", 3, true, 1 )
        if !passResult { t.Error(finalResultSummaryString) }
}

// 87
func Test_ORD87_1000000TX_3ch_3ord_kafka_3kbs_spy(t *testing.T) {
        passResult, finalResultSummaryString := ote("ORD-87", 1000000, 3, 3, "kafka", 3, true, 1 )
        if !passResult { t.Error(finalResultSummaryString) }
}

// 88
func Test_ORD88_1000000TX_1ch_1ord_kafka_3kbs_spy_3ppc(t *testing.T) {
        passResult, finalResultSummaryString := ote("ORD-88", 1000000, 1, 1, "kafka", 3, true, 3 )
        if !passResult { t.Error(finalResultSummaryString) }
}

// 89
func Test_ORD89_1000000TX_3ch_3ord_kafka_3kbs_spy_3ppc(t *testing.T) {
        passResult, finalResultSummaryString := ote("ORD-89", 1000000, 3, 3, "kafka", 3, true, 3 )
        if !passResult { t.Error(finalResultSummaryString) }
}

// 90
func Test_ORD90_1000000TX_100ch_1ord_kafka_3kbs_spy(t *testing.T) {
        passResult, finalResultSummaryString := ote("ORD-90", 1000000, 100, 1, "kafka", 3, true, 1 )
        if !passResult { t.Error(finalResultSummaryString) }
}

// 91
func Test_ORD91_1000000TX_100ch_3ord_kafka_3kbs(t *testing.T) {
        passResult, finalResultSummaryString := ote("ORD-91", 1000000, 100, 3, "kafka", 3, true, 1 )
        if !passResult { t.Error(finalResultSummaryString) }
}
