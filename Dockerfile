FROM golang:buster AS build
WORKDIR /go/src/glorp/
COPY . .
RUN go get -d -v
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o glorp .

FROM alpine:latest
RUN apk update
RUN apk add ncurses
COPY --from=build /go/src/glorp/glorp /go/bin/glorp
ENTRYPOINT ["/go/bin/glorp"]
