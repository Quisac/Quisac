## quisac
##
## make dev            — start server → http://localhost:8000
## make test           — run all tests
## make test/frontend/x
## make test/backend/x

.PHONY: dev test

dev:
	python3 src/main.py

test:
	@echo "── frontend ──"
	node --test tests/frontend/*.test.js
	@echo "── backend ──"
	pytest tests/backend/

test/frontend/%:
	node --test tests/frontend/$*.test.js

test/backend/%:
	pytest tests/backend/test_$*.py