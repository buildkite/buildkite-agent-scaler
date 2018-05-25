HANDLER ?= handler
PACKAGE ?= $(HANDLER)

setup:
	apt-get update -yq
	apt-get install -yq zip

build:
	GOOS=linux GOARCH=amd64 go build -o $(HANDLER) lambda/main.go
	zip $(PACKAGE).zip $(HANDLER)

.PHONY: build

clean:
	$(RM) $(HANDLER) $(PACKAGE).zip

.PHONY: clean

