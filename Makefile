DATABASE_URL ?= postgres://user:userpassword@localhost:5432/videothing?sslmode=disable

.PHONY: migrate-up migrate-down sqlc test

migrate-up:
	migrate -path packages/database/migrations -database "$(DATABASE_URL)" up

migrate-down:
	migrate -path packages/database/migrations -database "$(DATABASE_URL)" down 1

sqlc:
	cd packages/database && sqlc generate

test:
	go test ./...
