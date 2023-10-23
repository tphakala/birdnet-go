BINARY_DIR=bin
BINARY_NAME=birdnet

build:
	go build -ldflags "-s -w -X main.version=`date -u +%Y%m%d.%H%M%S`" -o ${BINARY_DIR}/${BINARY_NAME}

clean:
	go clean
	rm -f ${BINARY_DIR}/${BINARY_NAME}
