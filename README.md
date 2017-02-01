# Orderer Test Engine

## Follow these steps to install and execute tests

### Prerequisites
- <a href="https://git-scm.com/downloads" target="_blank">Git client</a>
- <a href="https://www.docker.com/products/overview" target="_blank">Docker v1.12 or higher</a>
- [Docker-Compose v1.8 or higher](https://docs.docker.com/compose/overview/)

Check your Docker and Docker-Compose versions with the following commands:
```bash
docker version
```
```bash
docker-compose version
```

### Clone the repo
```bash
git clone https://github.com/hyperledger-fabric.git
cd ./fabric/bddtests/regression/ote
```

### Execute Tests.
Use "go test" to execute a predefined set of functional tests.
```bash
go test
```

Run the Orderer Test Engine on command line.
There are several environment variables to control the test parameters,
such as number of transactions, number of orderers, ordererType, and more.
To see an example using default settings, simply execute the following. 
```bash
go build
./ote
```
Note the parameters will be displayed, and you can then choose to
set them to the settings of your choice. For example:
```bash
OTE_TXS=1000 OTE_CHANNELS=3 OTE_ORDERERS=3 ./ote
```

### Helpful Docker Commands

1. View running containers:

  ```
docker ps
```
2. View all containers (active and non-active):

  ```
docker ps -a
```
3. Stop all Docker containers:

  ```
docker stop $(docker ps -a -q)
```
4. Remove all containers.  Adding the `-f` will issue a "force" removal:

  ```
docker rm -f $(docker ps -aq)
```
5. Remove all images:

  ```
docker rmi -f $(docker images -q)
```
6. Remove all images except for hyperledger/fabric-baseimage:

  ```
docker rmi $(docker images | grep -v 'hyperledger/fabric-baseimage:latest' | awk {'print $3'})
```
7. Start a container:

  ```
docker start <containerID>
```
8. Stop a containerID:

  ```
docker stop <containerID>
```
9. View network settings for a specific container:

   ```
docker inspect <containerID>
```
10. View logs for a specific containerID:

  ```
docker logs -f <containerID>
```

