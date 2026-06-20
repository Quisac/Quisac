## quisac
##
## make dev            — start server → http://localhost:8000
## make test           — run all tests
## make test/frontend/x
## make test/backend/x
## make deploy         — push to main and deploy to Railway

.PHONY: dev test deploy

dev:
	go run src/main.go --serve

test:
	@echo "── frontend ──"
	node --test tests/frontend/*.test.js
	@echo "── backend ──"
	go test ./test/... -v

test/frontend/%:
	node --test tests/frontend/$*.test.js

test/backend/%:
	go test ./tests/backend/ -run $*

deploy:
	git push origin main
