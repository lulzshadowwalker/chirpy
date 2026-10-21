-- name: CreateChirp :one
insert into chirps (body, user_id)
values ($1, $2)
returning *;

-- name: GetChirps :many
select * from chirps
order by created_at desc;

-- name: GetChirpByID :one
select * from chirps
where id = $1
order by created_at desc;
