FROM segment/chamber:2.3 AS chamber
FROM golang:1.13 as builder
WORKDIR /go/src/github.com/buildkite/buildkite-agent-scaler
COPY . .
RUN GO111MODULE=off GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o buildkite-agent-scaler .

FROM 528451384384.dkr.ecr.us-west-2.amazonaws.com/segment-scratch
COPY --from=chamber /chamber /bin/chamber
COPY --from=builder /go/src/github.com/buildkite/buildkite-agent-scaler/buildkite-agent-scaler /bin/buildkite-agent-scaler
