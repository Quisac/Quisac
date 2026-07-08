## quisac
##
## make dev     — start server → http://localhost:8000
## make test    — run all Go tests
## make deploy  — push to main and deploy to Railway

.PHONY: dev up down test deploy

dev:
	go run ./cmd/quisac --serve

up:
	docker compose up --build

down:
	docker compose down

test:
	go test ./...

deploy:
	git push origin main
