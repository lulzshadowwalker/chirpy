-- name: CreateUser :one
insert into users (email, hashed_password)
values ($1, $2)
returning *;

-- name: UpdateUser :one
update users
set email = $1,
hashed_password = $2
where id = $3
returning *;

-- name: DeleteUsers :exec
delete from users;

-- name: GetUserByEmail :one
select * from users
where email = $1;
