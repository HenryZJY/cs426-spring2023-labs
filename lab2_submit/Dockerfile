# syntax=docker/dockerfile:1

FROM golang:1.19-alpine

WORKDIR /app

# Download Go modules
# COPY go.mod .
# COPY go.sum .
# RUN go mod download

COPY ./ ./

RUN go mod tidy
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /video-rec-service ./video_rec_service/server

EXPOSE 8080

CMD [ "/video-rec-service" ]