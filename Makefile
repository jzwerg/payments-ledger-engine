# Standard task interface across the portfolio: up / down / demo / test / logs.
.DEFAULT_GOAL := help
.PHONY: help up down logs ps demo test

help: ## List available targets
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-8s\033[0m %s\n", $$1, $$2}'

up: ## Boot the full stack
	docker compose up --build

down: ## Stop the stack and remove volumes
	docker compose down -v

logs: ## Tail service logs
	docker compose logs -f

ps: ## Show running services
	docker compose ps

demo: ## Headline demo — kill a node under load, assert the invariant holds (TODO: wire up — see MILESTONE.md)
	@echo "TODO: run the concurrent load test, kill a CockroachDB node mid-load, assert sum(debits)=sum(credits)."

test: ## Run the Go test suite
	go test ./...
