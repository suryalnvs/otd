
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

package ote        // Orderer Test Engine

import ( "fmt" )


// input args:  ote ( ordererType string, kbs int, txs int64, oUsed int, oInNtwk int, chans int )
// outputs:     print report to stdout with lots of counters!
// returns:     finalPassFailResult, finalResultSummaryString


/*
func Test_Solo_100000TX_1ch() {
        fmt.Println("START: Solo test: send 100,000 TX")
        _, _ := ote("solo", 0, 100000, 1, 1, 1 )
}
*/

func Test_kafka_3KBs_100000TX_1of1ord_1ch() {
        fmt.Println("START: Kafka test with 3 KBs, send 100,000 TX to 1 of a network of 1 Orderers, using 1 channel")
        _,_:= ote("kafka", 3, 100000, 1, 1, 1 )
}

func Test_kafka_3KBs_100000TX_3of3ord_1ch() {
        fmt.Println("START: Kafka test with 3 KBs, send 100,000 TX to 3 of a network of 3 Orderers, using 1 channel")
        _,_ = ote("kafka", 3, 100000, 3, 3, 1 )
}

