FROM golang:1.10-alpine3.7

RUN apk add -U ca-certificates curl git gcc musl-dev
RUN curl -fsSL -o /usr/local/bin/dep https://github.com/golang/dep/releases/download/v0.4.1/dep-linux-amd64 \
		&& chmod +x /usr/local/bin/dep

RUN mkdir -p $GOPATH/src/github.com/bgpat/godoc-walker
WORKDIR $GOPATH/src/github.com/bgpat/godoc-walker

COPY Gopkg.toml Gopkg.lock ./
RUN dep ensure -vendor-only -v

ADD . ./
RUN CGO_ENABLED=0 go build -ldflags="-s -w -extldflags '-static'" -o /godoc-walker


FROM golang:1.10-alpine3.7
RUN apk add -U --no-cache ca-certificates curl git
COPY --from=0 /godoc-walker /godoc-walker
ENTRYPOINT ["/godoc-walker"]
