BIN := gitlabcloner
DIST := dist

build:
	go build -o $(BIN) .

build-all:
	mkdir -p $(DIST)
	GOOS=darwin  GOARCH=arm64 go build -o $(DIST)/$(BIN)-darwin-arm64 .
	GOOS=darwin  GOARCH=amd64 go build -o $(DIST)/$(BIN)-darwin-amd64 .
	GOOS=linux   GOARCH=amd64 go build -o $(DIST)/$(BIN)-linux-amd64 .
	GOOS=linux   GOARCH=arm64 go build -o $(DIST)/$(BIN)-linux-arm64 .
	GOOS=windows GOARCH=amd64 go build -o $(DIST)/$(BIN)-windows-amd64.exe .

install:
	go install -v ./...

clean:
	rm -f $(BIN)
	rm -rf $(DIST)

.PHONY: build build-all install clean
