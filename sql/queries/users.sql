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

-- name: GetUserByID :one
select * from users
where id = $1;

-- name: MarkChirpyRed :one
update users 
set is_chirpy_red = true
where id = $1
returning *;
