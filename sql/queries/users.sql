-- name: CreateUser :one
insert into users (email)
values ($1)
returning *;

-- name: DeleteUsers :exec
delete from users;
