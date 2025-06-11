.PHONY: all clean build

all: build

clean:
	-rm -f handler.zip bootstrap

export CGO_ENABLED := 0

ifdef BUILDKITE_BUILD_NUMBER
  LD_FLAGS       := -s -w -X version.Build=$(BUILDKITE_BUILD_NUMBER)
  BUILDVSC_FLAG  := false
  USER           := 0:0
else
  LD_FLAGS       := -s -w
  BUILDVSC_FLAG  := true
  USER           := "$(shell id -u):$(shell id -g)"
endif

build: handler.zip

handler.zip: bootstrap
	zip -9 -v -j $@ "$<"

bootstrap: lambda/main.go
	docker run \
	  --env GOCACHE=/go/cache \
	  --env CGO_ENABLED \
	  --user $(USER) \
	  --volume $(PWD):/app \
	  --workdir /app \
	  --rm \
	  golang:1.24 \
	  go build \
	    -ldflags="$(LD_FLAGS)" \
	    -buildvcs="$(BUILDVSC_FLAG)" \
	    -tags lambda.norpc \
	    -o bootstrap \
	    ./lambda
