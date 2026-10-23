-- name: CreateUser :one
insert into users (email, hashed_password)
values ($1, $2)
returning *;

-- name: DeleteUsers :exec
delete from users;

-- name: GetUserByEmail :one
select * from users
where email = $1;
