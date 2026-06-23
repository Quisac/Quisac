FROM golang:1.22-bookworm

RUN curl -fsSL https://deb.nodesource.com/setup_20.x | bash - && apt-get install -y nodejs

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN cd html && npm install && npm run build
RUN CGO_ENABLED=1 go build -o out ./src/

EXPOSE 8080
CMD ["./out", "--serve"]