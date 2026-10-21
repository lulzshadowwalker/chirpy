default:
    @just --list

run:
    go run .

set dotenv-load := true

DB_URL := env_var("DB_URL")
MIGRATIONS_DIR := "sql/schema"

migrate-up:
    goose --dir {{ MIGRATIONS_DIR }} postgres "{{ DB_URL }}" up

migrate-down:
    goose --dir {{ MIGRATIONS_DIR }} postgres "{{ DB_URL }}" down

migrate-status:
    goose --dir {{ MIGRATIONS_DIR }} postgres "{{ DB_URL }}" status

migrate-reset:
    goose --dir {{ MIGRATIONS_DIR }} postgres "{{ DB_URL }}" reset

migrate-fresh:
    goose --dir {{ MIGRATIONS_DIR }} postgres "{{ DB_URL }}" reset
    goose --dir {{ MIGRATIONS_DIR }} postgres "{{ DB_URL }}" up

migrate-create name:
    goose --dir {{ MIGRATIONS_DIR }} create {{ name }} sql
