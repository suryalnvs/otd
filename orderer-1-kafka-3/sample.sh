nmessage=$1


cd /opt/gopath/src/github.com/hyperledger/fabric/orderer/sample_clients/deliver_stdout
go build
echo "starting deliver client"
./deliver_stdout -server 127.0.0.1:7050  | grep "Data:\"" | wc -c > count.txt &2>1 
echo "broadcast"
cd ../broadcast_timestamp
go build
./broadcast_timestamp -server 127.0.0.1:7050 -messages ${nmessage}





