FROM golang:1.26-bookworm

# Install Node.js
RUN curl -fsSL https://deb.nodesource.com/setup_20.x | bash - && apt-get install -y nodejs

RUN curl -fsSL https://deb.nodesource.com/setup_20.x | bash - && apt-get install -y nodejs

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
<<<<<<< HEAD
RUN cd html && npm install && npm run build
RUN CGO_ENABLED=1 go build -o out ./src/

EXPOSE 8080
CMD ["./out", "--serve"]
=======

# Build frontend
RUN cd html && npm install && npm run build

# Build backend
RUN CGO_ENABLED=1 go build -o out ./src/

EXPOSE 8080
CMD ["./out", "--serve"]
>>>>>>> 7a9eeb609e7692bb4b2e0d46d3d1aae03eca9b91
