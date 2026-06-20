## quisac
##
## make dev            — start server → http://localhost:8000
## make test           — run all tests
## make test/frontend/x
## make test/backend/x
.PHONY: dev test

dev:
	go run src/main.go

test:
	@echo "── frontend ──"
	node --test tests/frontend/*.test.js
	@echo "── backend ──"
	go test ./test/... -v

test/frontend/%:
	node --test tests/frontend/$*.test.js

test/backend/%:
	go test ./tests/backend/ -run $*
