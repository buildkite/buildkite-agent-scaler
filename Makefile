.PHONY: all clean build build-arm64

all: build

build: handler.zip handler-arm64.zip

clean:
	-rm -f handler.zip handler-arm64.zip bootstrap bootstrap-arm64

export CGO_ENABLED := 0
export GOOS := linux

ifdef BUILDKITE_BUILD_NUMBER
  LD_FLAGS       := -s -w -X version.Build=$(BUILDKITE_BUILD_NUMBER)
  BUILDVCS_FLAG  := false
  USER           := 0:0
else
  LD_FLAGS       := -s -w
  BUILDVCS_FLAG  := true
  USER           := "$(shell id -u):$(shell id -g)"
endif

build-arm64: handler-arm64.zip

# handler.zip is x86_64 (default/existing architecture)
handler.zip: bootstrap
	zip -9 -v -j $@ "$<"

handler-arm64.zip: bootstrap-arm64
	@rm -f bootstrap
	@cp bootstrap-arm64 bootstrap
	@zip -9 -v -j $@ bootstrap
	@rm -f bootstrap

bootstrap: lambda/main.go
	./bin/mise install
	GOARCH=amd64 ./bin/mise exec -- go build \
	    -ldflags="$(LD_FLAGS)" \
	    -buildvcs="$(BUILDVCS_FLAG)" \
	    -tags lambda.norpc \
	    -o bootstrap \
	    ./lambda

bootstrap-arm64: lambda/main.go
	./bin/mise install
	GOARCH=arm64 ./bin/mise exec -- go build \
	    -ldflags="$(LD_FLAGS)" \
	    -buildvcs="$(BUILDVCS_FLAG)" \
	    -tags lambda.norpc \
	    -o bootstrap-arm64 \
	    ./lambda
