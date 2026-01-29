.PHONY: up down test lint fmt

up:
	docker compose up postgres -d --wait

down:
	docker compose down -v

test:
	docker compose run --rm test

lint:
	docker compose run --rm lint

fmt:
	docker compose run --rm fmt
