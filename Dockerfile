FROM golang:1.22-bookworm

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=1 go build -o out ./src/

EXPOSE 8080
CMD ["./out"]
