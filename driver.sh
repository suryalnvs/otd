#!/bin/bash

#OS
OSName=`uname`
echo "Operating System: $OSName"

Req=$1
InvalidArgs=0

if [ $Req != "create" ] && [ $Req != "add" ]; then
    InvalidArgs=1
    echo "invalid Req=$Req"
fi

echo "number of args=$#"
    if [ $# -lt 4 ] || [ $# -gt 5 ]; then
       InvalidArgs=1
    echo "invalid number of args=$#"
    fi

#sanity check input args
if [ $InvalidArgs == 1 ]; then
     echo "error: invalid input parameter: $Req"
     echo ""
     echo "Usage:"
     echo "      ./driver_kafka [create|add] [nPeer] [nOrderer] [nBroker] [db]"
     echo "      nPeer: number of peer"
     echo "      nOrderer: number of orderer"
     echo "      nBroker: number of kafka broker, if 0, then solo is applied"
     echo "      db: db type"
     echo "             couch - couchDB"
     echo "             level - golevelDB"
     echo "      note: 1 cop is included as default"
     echo ""
     echo "      ex. ./driver_opt.sh create 2 4 0"
     echo "            create a network with 2 peers, 4 ordererer with solo"
     echo ""
     echo "      ex. ./driver_opt.sh create 2 4 3"
     echo "            create a network with 2 peers, 4 ordererer and 3 kafka"
     echo ""
     echo "      ex. ./driver_opt.sh add 2 4 3"
     echo "            add 2 peers to the network"
     echo ""
     exit

fi


nPeer=$2
nOrderer=$3
nBroker=$4
db=$5

dbType=`echo "$db" | awk '{print tolower($0)}'`
echo "action=$Req nPeer=$nPeer nBroker=$nBroker nOrderer=$nOrderer dbType=$dbType"
VP=`docker ps -a | grep 'peer node start' | wc -l`
echo "existing peers: $VP"


echo "remove old docker-composer.yml"
rm -f docker-compose.yml

echo "docker pull https://hub.docker.com/r/rameshthoomu/fabric-ccenv-x86_64"
docker pull rameshthoomu/fabric-ccenv-x86_64

# form json input file
if [ $nBroker == 0 ]; then
    #jsonFILE="network_solo.json"
    jsonFILE="network.json"
else
#    jsonFILE="network_kafka.json"
    jsonFILE="network.json"
fi
echo "jsonFILE $jsonFILE"

# create docker compose yml
if [ $Req == "add" ]; then
    N1=$[nPeer+VP]
    N=$[N1]
    VPN="peer"$[N-1]
else
    N1=$nPeer
    N=$[N1 - 1]
    VPN="peer"$N
fi

echo "N1=$N1 VP=$VP nPeer=$nPeer VPN=$VPN"

node json2yml.js $jsonFILE $N1 $nOrderer $nBroker $dbType

## sed 's/-x86_64/TEST/g' docker-compose.yml > ss.yml
## cp ss.yml docker-compose.yml
# create network
if [ $Req == "create" ]; then

   docker-compose -f docker-compose.yml up -d --force-recreate $VPN
   ##docker-compose -f docker-compose.yml up -d --force-recreate $VPN
   for ((i=1; i<$nOrderer; i++))
   do
       tmpOrd="orderer"$i
       docker-compose -f docker-compose.yml up -d $tmpOrd
   done
fi

if [ $Req == "add" ]; then
   docker-compose -f docker-compose.yml up -d $VPN

fi

exit
