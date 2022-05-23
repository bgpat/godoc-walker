FROM golang:1.14-alpine3.11

RUN apk add -U ca-certificates curl git gcc musl-dev

WORKDIR $GOPATH/src/github.com/bgpat/godoc-walker

COPY go.mod go.sum ./
RUN go mod download

ADD . ./
RUN CGO_ENABLED=0 go build -ldflags="-s -w -extldflags '-static'" -o /godoc-walker


FROM alpine:3.16
RUN apk add -U --no-cache ca-certificates curl git
COPY --from=0 /godoc-walker /godoc-walker
ENTRYPOINT ["/godoc-walker"]
