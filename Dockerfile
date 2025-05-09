FROM golang:1.24-bookworm AS build
WORKDIR /go/src/glorp/
COPY . .
RUN go get -v
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o glorp -overlay overlay.23.json .

FROM alpine:latest
RUN apk update
RUN apk add ncurses vim
RUN rm /usr/bin/vi && ln -s /usr/bin/vim /usr/bin/vi
COPY --from=build /go/src/glorp/glorp /go/bin/glorp
ENTRYPOINT ["/go/bin/glorp"]
