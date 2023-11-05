BINARY_DIR=bin
BINARY_NAME=birdnet

build:
	CGO_ENABLED=1 CGO_CFLAGS="-I$(HOME)/src/tensorflow" go build -ldflags "-s -w" -o ${BINARY_DIR}/${BINARY_NAME}

windows:
	GOOS=windows GOARCH=amd64 CGO_ENABLED=1 CC=x86_64-w64-mingw32-gcc CGO_CFLAGS="-I$(HOME)/src/tensorflow" go build -ldflags "-s -w" -o $(BINARY_DIR)/$(BINARY_NAME).exe

clean:
	go clean
	rm -f ${BINARY_DIR}/${BINARY_NAME}
