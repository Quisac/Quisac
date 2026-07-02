## quisac
##
## make dev     — start server → http://localhost:8000
## make test    — run all Go tests
## make deploy  — push to main and deploy to Railway

.PHONY: dev test deploy

dev:
	go run ./cmd/quisac --serve

test:
	go test ./...

deploy:
	git push origin main
