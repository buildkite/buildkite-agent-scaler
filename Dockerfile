FROM golang:1.15 as builder
WORKDIR /go/src/github.com/buildkite/buildkite-agent-scaler
COPY . .
RUN GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o buildkite-agent-scaler .

FROM 528451384384.dkr.ecr.us-west-2.amazonaws.com/segment-scratch
COPY --from=builder /go/src/github.com/buildkite/buildkite-agent-scaler/buildkite-agent-scaler /bin/buildkite-agent-scaler
