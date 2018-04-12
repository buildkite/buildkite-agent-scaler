HANDLER ?= handler
PACKAGE ?= $(HANDLER)

build:
	GOOS=linux GOARCH=amd64 go build -o $(HANDLER) lambda.go
	zip $(PACKAGE).zip $(HANDLER)

.PHONY: build

clean:
	$(RM) $(HANDLER) $(PACKAGE).zip

.PHONY: clean

